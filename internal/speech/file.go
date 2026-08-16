package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultFileChunkRunes = 1200
	MaxProviderFrameBytes = 6000
	maxPCMBytesPerFile    = 64 * 1024 * 1024
)

var (
	speechFencedCode = regexp.MustCompile("(?s)```.*?```")
	speechImageLink  = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	speechLink       = regexp.MustCompile(`\[([^\]]+)\]\((?:https?://|/)[^)]*\)`)
	speechEmail      = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,24}\b`)
	speechURL        = regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s<>()\[\]{}，。！？；：、]+`)
	speechDomain     = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9\-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}(?:/[^\s<>()\[\]{}，。！？；：、]+)?`)
	speechHTML       = regexp.MustCompile(`<[^>]+>`)
	speechInlineCode = regexp.MustCompile("`([^`]+)`")
	speechHeading    = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	speechQuote      = regexp.MustCompile(`(?m)^\s*>\s?`)
	speechBullet     = regexp.MustCompile(`(?m)^\s*[-*+]\s+`)
	speechNumbered   = regexp.MustCompile(`(?m)^\s*\d+[.)、]\s+`)
	speechTableRule  = regexp.MustCompile(`(?m)^\s*\|?[\s:|\-]+\|?\s*$`)
	speechCitation   = regexp.MustCompile(`(?i)\[(?:\d+|[a-z])\]`)
	speechSpaces     = regexp.MustCompile(`[ \t]+`)
	speechNewlines   = regexp.MustCompile(`\s*\n+\s*`)
	speechPeriods    = regexp.MustCompile(`。{2,}`)
)

func NormalizeAnswerText(value string) string {
	if value == "" {
		return ""
	}
	text := strings.ReplaceAll(value, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if strings.Count(text, "```")%2 == 1 {
		if open := strings.LastIndex(text, "```"); open >= 0 {
			text = text[:open]
		}
	}
	text = speechFencedCode.ReplaceAllString(text, "\n代码内容已省略。\n")
	text = speechImageLink.ReplaceAllString(text, "$1")
	text = speechLink.ReplaceAllString(text, "$1")
	text = speechEmail.ReplaceAllString(text, " ")
	text = speechURL.ReplaceAllString(text, " ")
	text = speechDomain.ReplaceAllString(text, " ")
	text = speechHTML.ReplaceAllString(text, " ")
	text = speechInlineCode.ReplaceAllString(text, "$1")
	text = speechHeading.ReplaceAllString(text, "")
	text = speechQuote.ReplaceAllString(text, "")
	text = speechBullet.ReplaceAllString(text, "")
	text = speechNumbered.ReplaceAllString(text, "")
	text = speechTableRule.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "|", "，")
	text = speechCitation.ReplaceAllString(text, "")
	text = strings.NewReplacer("*", "", "_", "", "~", "").Replace(text)
	text = speechSpaces.ReplaceAllString(text, " ")
	text = speechNewlines.ReplaceAllString(text, "。")
	text = speechPeriods.ReplaceAllString(text, "。")
	text = strings.TrimSpace(text)
	return text
}

func SplitAnswerText(value string, maximumRunes int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if maximumRunes < 1 {
		maximumRunes = DefaultFileChunkRunes
	}
	runes := []rune(value)
	chunks := make([]string, 0, len(runes)/maximumRunes+1)
	for len(runes) > 0 {
		if len(runes) <= maximumRunes {
			chunks = append(chunks, strings.TrimSpace(string(runes)))
			break
		}
		cut := maximumRunes
		for index := maximumRunes; index > maximumRunes/2; index-- {
			if isSpeechBoundary(runes[index-1]) {
				cut = index
				break
			}
		}
		chunk := strings.TrimSpace(string(runes[:cut]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[cut:]
		for len(runes) > 0 && unicode.IsSpace(runes[0]) {
			runes = runes[1:]
		}
	}
	return chunks
}

func SplitUTF8Frames(value string, maximumBytes int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if maximumBytes < 1 {
		maximumBytes = MaxProviderFrameBytes
	}
	frames := make([]string, 0, len(value)/maximumBytes+1)
	var current strings.Builder
	currentBytes := 0
	for _, character := range value {
		characterBytes := utf8.RuneLen(character)
		if characterBytes < 0 {
			characterBytes = len(string(character))
		}
		if currentBytes+characterBytes > maximumBytes && current.Len() > 0 {
			frames = append(frames, strings.TrimSpace(current.String()))
			current.Reset()
			currentBytes = 0
		}
		current.WriteRune(character)
		currentBytes += characterBytes
	}
	if frame := strings.TrimSpace(current.String()); frame != "" {
		frames = append(frames, frame)
	}
	return frames
}

func SynthesizePCM(
	ctx context.Context,
	provider Provider,
	config SessionConfig,
	text string,
) ([]byte, AudioConfig, error) {
	if provider == nil || !provider.Configured() {
		return nil, AudioConfig{}, ErrProviderUnavailable
	}
	session, err := provider.Open(ctx, config)
	if err != nil {
		return nil, AudioConfig{}, err
	}
	defer session.Close()
	audioConfig := session.AudioConfig()
	if audioConfig.Format != "pcm" ||
		audioConfig.SampleRate < 8000 ||
		audioConfig.Channels != 1 ||
		audioConfig.BitDepth != 16 {
		return nil, AudioConfig{}, errors.New("unsupported speech audio format")
	}
	frames := SplitUTF8Frames(text, MaxProviderFrameBytes)
	if len(frames) == 0 {
		return nil, AudioConfig{}, errors.New("speech text is empty")
	}
	for _, frame := range frames {
		if err := session.SendText(ctx, frame); err != nil {
			return nil, AudioConfig{}, err
		}
	}
	if err := session.Finish(ctx); err != nil {
		return nil, AudioConfig{}, err
	}
	var pcm bytes.Buffer
	for {
		event, err := session.ReadEvent(ctx)
		if err != nil {
			return nil, AudioConfig{}, err
		}
		switch event.Type {
		case EventAudio:
			if len(event.Audio) == 0 {
				continue
			}
			if pcm.Len()+len(event.Audio) > maxPCMBytesPerFile {
				return nil, AudioConfig{}, errors.New("speech audio file is too large")
			}
			_, _ = pcm.Write(event.Audio)
		case EventCompleted:
			if pcm.Len() == 0 {
				return nil, AudioConfig{}, errors.New("speech provider returned no audio")
			}
			if pcm.Len()%2 != 0 {
				return nil, AudioConfig{}, errors.New("speech provider returned misaligned pcm")
			}
			return pcm.Bytes(), audioConfig, nil
		}
	}
}

func PCMToWAV(pcm []byte, config AudioConfig) ([]byte, error) {
	if config.Format != "pcm" ||
		config.SampleRate < 8000 ||
		config.Channels != 1 ||
		config.BitDepth != 16 {
		return nil, errors.New("unsupported pcm format")
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil, errors.New("pcm data is empty or misaligned")
	}
	if len(pcm) > int(^uint32(0))-36 {
		return nil, errors.New("pcm data is too large for wav")
	}
	var output bytes.Buffer
	output.Grow(44 + len(pcm))
	output.WriteString("RIFF")
	_ = binary.Write(&output, binary.LittleEndian, uint32(36+len(pcm)))
	output.WriteString("WAVE")
	output.WriteString("fmt ")
	_ = binary.Write(&output, binary.LittleEndian, uint32(16))
	_ = binary.Write(&output, binary.LittleEndian, uint16(1))
	_ = binary.Write(
		&output,
		binary.LittleEndian,
		uint16(config.Channels),
	)
	_ = binary.Write(
		&output,
		binary.LittleEndian,
		uint32(config.SampleRate),
	)
	bytesPerSample := config.BitDepth / 8
	byteRate := config.SampleRate * config.Channels * bytesPerSample
	blockAlign := config.Channels * bytesPerSample
	_ = binary.Write(&output, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&output, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(
		&output,
		binary.LittleEndian,
		uint16(config.BitDepth),
	)
	output.WriteString("data")
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(pcm)))
	_, _ = output.Write(pcm)
	return output.Bytes(), nil
}

func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create audio directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".audio-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary audio: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := io.Copy(temporary, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("write temporary audio: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary audio: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary audio: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish audio file: %w", err)
	}
	return nil
}

func isSpeechBoundary(value rune) bool {
	return strings.ContainsRune("。！？!?；;\n", value)
}
