package speech

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeAnswerTextRemovesUnsafeAndUnspokenMarkup(t *testing.T) {
	input := "# 菜单建议\n\n" +
		"- [红烧肉](https://example.com/recipe) 30 元\n" +
		"- 联系 chef@example.com 或访问 https://example.com/private\n" +
		"- <strong>提示</strong>：使用 `低温`。\n\n" +
		"```go\nsecret := \"do not read\"\n```\n\n" +
		"来源：[1] example.org/path"
	got := NormalizeAnswerText(input)
	for _, expected := range []string{"菜单建议", "红烧肉", "30 元", "提示", "低温", "代码内容已省略"} {
		if !strings.Contains(got, expected) {
			t.Errorf("NormalizeAnswerText() lacks %q: %q", expected, got)
		}
	}
	for _, forbidden := range []string{
		"https://",
		"chef@example.com",
		"example.org",
		"secret :=",
		"<strong>",
		"```",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("NormalizeAnswerText() contains %q: %q", forbidden, got)
		}
	}
}

func TestNormalizeAnswerTextDropsUnclosedFencedCode(t *testing.T) {
	got := NormalizeAnswerText("可朗读内容。\n```json\n{\"secret\":true}")
	if got != "可朗读内容。" {
		t.Fatalf("NormalizeAnswerText(unclosed fence) = %q", got)
	}
}

func TestSplitAnswerTextUsesSentenceBoundaryAndHandlesLongSentence(t *testing.T) {
	got := SplitAnswerText("第一句很短。第二句也不长。第三句结束。", 12)
	if len(got) < 2 || strings.Join(got, "") != "第一句很短。第二句也不长。第三句结束。" {
		t.Fatalf("SplitAnswerText(sentence boundaries) = %#v", got)
	}
	for _, chunk := range got {
		if utf8.RuneCountInString(chunk) > 12 {
			t.Errorf("chunk exceeds rune limit: %q", chunk)
		}
	}

	long := strings.Repeat("菜", 29)
	got = SplitAnswerText(long, 10)
	if len(got) != 3 ||
		utf8.RuneCountInString(got[0]) != 10 ||
		utf8.RuneCountInString(got[1]) != 10 ||
		utf8.RuneCountInString(got[2]) != 9 ||
		strings.Join(got, "") != long {
		t.Fatalf("SplitAnswerText(long sentence) = %#v", got)
	}
}

func TestSplitUTF8FramesNeverSplitsRunes(t *testing.T) {
	value := strings.Repeat("菜A", 12)
	frames := SplitUTF8Frames(value, 7)
	if strings.Join(frames, "") != value {
		t.Fatalf("frames do not reconstruct input: %#v", frames)
	}
	for _, frame := range frames {
		if !utf8.ValidString(frame) || len(frame) > 7 {
			t.Errorf("invalid frame %q (%d bytes)", frame, len(frame))
		}
	}
}

func TestPCMToWAVWritesStandardHeader(t *testing.T) {
	pcm := []byte{1, 2, 3, 4, 5, 6}
	wav, err := PCMToWAV(pcm, AudioConfig{
		Format: "pcm", SampleRate: 24000, Channels: 1, BitDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) != 44+len(pcm) ||
		string(wav[:4]) != "RIFF" ||
		string(wav[8:12]) != "WAVE" ||
		string(wav[12:16]) != "fmt " ||
		string(wav[36:40]) != "data" {
		t.Fatalf("invalid WAV header: %x", wav[:44])
	}
	if binary.LittleEndian.Uint32(wav[4:8]) != uint32(36+len(pcm)) ||
		binary.LittleEndian.Uint32(wav[24:28]) != 24000 ||
		binary.LittleEndian.Uint32(wav[28:32]) != 48000 ||
		binary.LittleEndian.Uint16(wav[22:24]) != 1 ||
		binary.LittleEndian.Uint16(wav[34:36]) != 16 ||
		binary.LittleEndian.Uint32(wav[40:44]) != uint32(len(pcm)) {
		t.Fatalf("incorrect WAV fields: %x", wav[:44])
	}
	if string(wav[44:]) != string(pcm) {
		t.Fatalf("WAV PCM = %x, want %x", wav[44:], pcm)
	}
}

func TestPCMToWAVRejectsUnsupportedOrMisalignedInput(t *testing.T) {
	for _, test := range []struct {
		pcm    []byte
		config AudioConfig
	}{
		{nil, AudioConfig{Format: "pcm", SampleRate: 24000, Channels: 1, BitDepth: 16}},
		{[]byte{1}, AudioConfig{Format: "pcm", SampleRate: 24000, Channels: 1, BitDepth: 16}},
		{[]byte{1, 2}, AudioConfig{Format: "mp3", SampleRate: 24000, Channels: 1, BitDepth: 16}},
		{[]byte{1, 2}, AudioConfig{Format: "pcm", SampleRate: 24000, Channels: 2, BitDepth: 16}},
		{[]byte{1, 2}, AudioConfig{Format: "pcm", SampleRate: 24000, Channels: 1, BitDepth: 8}},
	} {
		if _, err := PCMToWAV(test.pcm, test.config); err == nil {
			t.Fatalf("PCMToWAV(%#v, %#v) succeeded", test.pcm, test.config)
		}
	}
}

func TestSynthesizePCMStreamsUTF8FramesAndCollectsAudio(t *testing.T) {
	session := &fileTestSession{
		config: AudioConfig{
			Format: "pcm", SampleRate: 24000, Channels: 1, BitDepth: 16,
		},
		events: []Event{
			{Type: EventAudio, Audio: []byte{1, 2}},
			{Type: EventAudio, Audio: []byte{3, 4}},
			{Type: EventCompleted},
		},
	}
	provider := &fileTestProvider{session: session}
	text := strings.Repeat("菜", MaxProviderFrameBytes)
	pcm, config, err := SynthesizePCM(
		context.Background(),
		provider,
		SessionConfig{Voice: "voice", Speed: 1.1},
		text,
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(pcm) != string([]byte{1, 2, 3, 4}) ||
		config.SampleRate != 24000 ||
		!session.finished ||
		!session.closed ||
		len(session.sent) < 2 {
		t.Fatalf(
			"pcm=%v config=%#v sent=%d finished=%v closed=%v",
			pcm,
			config,
			len(session.sent),
			session.finished,
			session.closed,
		)
	}
	for _, frame := range session.sent {
		if len(frame) > MaxProviderFrameBytes || !utf8.ValidString(frame) {
			t.Errorf("invalid provider frame (%d bytes)", len(frame))
		}
	}
}

func TestSynthesizePCMRejectsIncompleteProviderOutput(t *testing.T) {
	tests := []struct {
		name    string
		session *fileTestSession
	}{
		{
			name: "no audio",
			session: &fileTestSession{
				config: standardFileTestAudioConfig(),
				events: []Event{{Type: EventCompleted}},
			},
		},
		{
			name: "misaligned audio",
			session: &fileTestSession{
				config: standardFileTestAudioConfig(),
				events: []Event{
					{Type: EventAudio, Audio: []byte{1}},
					{Type: EventCompleted},
				},
			},
		},
		{
			name: "unsupported format",
			session: &fileTestSession{
				config: AudioConfig{
					Format: "mp3", SampleRate: 24000, Channels: 1, BitDepth: 16,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := SynthesizePCM(
				context.Background(),
				&fileTestProvider{session: test.session},
				SessionConfig{Voice: "voice", Speed: 1},
				"要朗读的文字",
			)
			if err == nil {
				t.Fatal("SynthesizePCM() succeeded")
			}
			if !test.session.closed {
				t.Fatal("provider session was not closed")
			}
		})
	}
}

func TestWriteFileAtomicPublishesPrivateCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "answer.wav")
	data := []byte("complete audio")
	if err := WriteFileAtomic(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("file = %q, want %q", got, data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "answer.wav" {
		t.Fatalf("published directory entries = %#v", entries)
	}
}

type fileTestProvider struct {
	session *fileTestSession
	openErr error
}

func (p *fileTestProvider) ID() string       { return "file-test" }
func (p *fileTestProvider) Configured() bool { return true }
func (p *fileTestProvider) Voices() []Voice  { return nil }
func (p *fileTestProvider) Open(context.Context, SessionConfig) (Session, error) {
	if p.openErr != nil {
		return nil, p.openErr
	}
	if p.session == nil {
		return nil, errors.New("missing test session")
	}
	return p.session, nil
}

type fileTestSession struct {
	config    AudioConfig
	events    []Event
	sent      []string
	sendErr   error
	finishErr error
	readErr   error
	finished  bool
	closed    bool
}

func (s *fileTestSession) AudioConfig() AudioConfig { return s.config }
func (s *fileTestSession) SendText(_ context.Context, value string) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, value)
	return nil
}
func (s *fileTestSession) Finish(context.Context) error {
	if s.finishErr != nil {
		return s.finishErr
	}
	s.finished = true
	return nil
}
func (s *fileTestSession) ReadEvent(context.Context) (Event, error) {
	if s.readErr != nil {
		return Event{}, s.readErr
	}
	if len(s.events) == 0 {
		return Event{}, errors.New("no test event")
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}
func (s *fileTestSession) Close() error {
	s.closed = true
	return nil
}

func standardFileTestAudioConfig() AudioConfig {
	return AudioConfig{
		Format: "pcm", SampleRate: 24000, Channels: 1, BitDepth: 16,
	}
}
