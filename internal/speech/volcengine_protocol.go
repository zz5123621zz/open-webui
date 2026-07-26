package speech

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// Values and field order follow the protocol attachment published with
	// Volcengine's V3 bidirectional TTS documentation.
	volcProtocolVersion = 1
	volcHeaderWords     = 1

	volcMsgFullClientRequest  = 0x1
	volcMsgFullServerResponse = 0x9
	volcMsgAudioOnlyServer    = 0xb
	volcMsgError              = 0xf

	volcFlagNoSequence = 0x0
	volcFlagPositive   = 0x1
	volcFlagNegative   = 0x3
	volcFlagWithEvent  = 0x4

	volcSerializationRaw  = 0x0
	volcSerializationJSON = 0x1

	volcCompressionNone = 0x0
	volcCompressionGzip = 0x1

	volcEventStartConnection    = 1
	volcEventFinishConnection   = 2
	volcEventConnectionStarted  = 50
	volcEventConnectionFailed   = 51
	volcEventConnectionFinished = 52
	volcEventStartSession       = 100
	volcEventCancelSession      = 101
	volcEventFinishSession      = 102
	volcEventSessionStarted     = 150
	volcEventSessionCanceled    = 151
	volcEventSessionFinished    = 152
	volcEventSessionFailed      = 153
	volcEventUsageResponse      = 154
	volcEventTaskRequest        = 200
	volcEventTTSSentenceStart   = 350
	volcEventTTSSentenceEnd     = 351
	volcEventTTSResponse        = 352
	volcEventTTSSubtitle        = 364

	maxVolcProviderFrameBytes = 10 * 1024 * 1024
)

type volcMessage struct {
	MessageType   uint8
	Flag          uint8
	Serialization uint8
	Compression   uint8
	Event         int32
	SessionID     string
	ConnectID     string
	Sequence      int32
	ErrorCode     uint32
	Payload       []byte
}

func marshalVolcClientEvent(event int32, sessionID string, payload []byte) ([]byte, error) {
	return marshalVolcMessage(volcMessage{
		MessageType:   volcMsgFullClientRequest,
		Flag:          volcFlagWithEvent,
		Serialization: volcSerializationJSON,
		Compression:   volcCompressionNone,
		Event:         event,
		SessionID:     sessionID,
		Payload:       payload,
	})
}

func marshalVolcMessage(message volcMessage) ([]byte, error) {
	if len(message.SessionID) > maxVolcProviderFrameBytes ||
		len(message.ConnectID) > maxVolcProviderFrameBytes ||
		len(message.Payload) > maxVolcProviderFrameBytes {
		return nil, errors.New("Volcengine speech frame is too large")
	}
	var buffer bytes.Buffer
	buffer.WriteByte(byte(volcProtocolVersion<<4 | volcHeaderWords))
	buffer.WriteByte(byte(message.MessageType<<4 | message.Flag))
	buffer.WriteByte(byte(message.Serialization<<4 | message.Compression))
	buffer.WriteByte(0)

	if message.Flag == volcFlagPositive || message.Flag == volcFlagNegative {
		_ = binary.Write(&buffer, binary.BigEndian, message.Sequence)
	}
	if message.MessageType == volcMsgError {
		_ = binary.Write(&buffer, binary.BigEndian, message.ErrorCode)
	}
	if message.Flag == volcFlagWithEvent {
		_ = binary.Write(&buffer, binary.BigEndian, message.Event)
		if !volcConnectionEvent(message.Event) {
			if err := writeVolcBytes(&buffer, []byte(message.SessionID)); err != nil {
				return nil, err
			}
		} else if volcDownstreamConnectionEvent(message.Event) {
			if err := writeVolcBytes(&buffer, []byte(message.ConnectID)); err != nil {
				return nil, err
			}
		}
	}
	if err := writeVolcBytes(&buffer, message.Payload); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func unmarshalVolcMessage(data []byte) (volcMessage, error) {
	if len(data) < 4 {
		return volcMessage{}, errors.New("Volcengine speech frame is shorter than its header")
	}
	version := data[0] >> 4
	headerWords := data[0] & 0x0f
	if version != volcProtocolVersion || headerWords < 1 {
		return volcMessage{}, fmt.Errorf(
			"unsupported Volcengine speech protocol header (%d/%d)",
			version, headerWords,
		)
	}
	headerBytes := int(headerWords) * 4
	if headerBytes > len(data) {
		return volcMessage{}, errors.New("Volcengine speech frame has an invalid header size")
	}
	message := volcMessage{
		MessageType:   data[1] >> 4,
		Flag:          data[1] & 0x0f,
		Serialization: data[2] >> 4,
		Compression:   data[2] & 0x0f,
	}
	if message.Serialization != volcSerializationRaw &&
		message.Serialization != volcSerializationJSON {
		return volcMessage{}, errors.New("unsupported Volcengine speech serialization")
	}
	reader := bytes.NewReader(data[headerBytes:])
	var err error
	if message.Flag == volcFlagPositive || message.Flag == volcFlagNegative {
		err = binary.Read(reader, binary.BigEndian, &message.Sequence)
	}
	if err == nil && message.MessageType == volcMsgError {
		err = binary.Read(reader, binary.BigEndian, &message.ErrorCode)
	}
	if err == nil && message.Flag == volcFlagWithEvent {
		err = binary.Read(reader, binary.BigEndian, &message.Event)
		if err == nil && !volcConnectionEvent(message.Event) {
			message.SessionID, err = readVolcString(reader)
		} else if err == nil && volcDownstreamConnectionEvent(message.Event) {
			message.ConnectID, err = readVolcString(reader)
		}
	}
	if err != nil {
		return volcMessage{}, fmt.Errorf("decode Volcengine speech frame header: %w", err)
	}
	message.Payload, err = readVolcBytes(reader)
	if err != nil {
		return volcMessage{}, fmt.Errorf("decode Volcengine speech payload: %w", err)
	}
	if reader.Len() != 0 {
		return volcMessage{}, errors.New("Volcengine speech frame has trailing data")
	}
	switch message.Compression {
	case volcCompressionNone:
	case volcCompressionGzip:
		compressed := bytes.NewReader(message.Payload)
		gzipReader, gzipErr := gzip.NewReader(compressed)
		if gzipErr != nil {
			return volcMessage{}, fmt.Errorf("open Volcengine gzip payload: %w", gzipErr)
		}
		decompressed, readErr := io.ReadAll(io.LimitReader(
			gzipReader, maxVolcProviderFrameBytes+1,
		))
		closeErr := gzipReader.Close()
		if readErr != nil {
			return volcMessage{}, fmt.Errorf("read Volcengine gzip payload: %w", readErr)
		}
		if closeErr != nil {
			return volcMessage{}, fmt.Errorf("close Volcengine gzip payload: %w", closeErr)
		}
		if len(decompressed) > maxVolcProviderFrameBytes {
			return volcMessage{}, errors.New("Volcengine gzip payload is too large")
		}
		message.Payload = decompressed
	default:
		return volcMessage{}, errors.New("unsupported Volcengine speech compression")
	}
	return message, nil
}

func writeVolcBytes(buffer *bytes.Buffer, value []byte) error {
	if len(value) > maxVolcProviderFrameBytes {
		return errors.New("Volcengine speech field is too large")
	}
	if err := binary.Write(buffer, binary.BigEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := buffer.Write(value)
	return err
}

func readVolcString(reader *bytes.Reader) (string, error) {
	value, err := readVolcBytes(reader)
	return string(value), err
}

func readVolcBytes(reader *bytes.Reader) ([]byte, error) {
	var size uint32
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
		return nil, err
	}
	if size > maxVolcProviderFrameBytes || uint64(size) > uint64(reader.Len()) {
		return nil, errors.New("Volcengine speech field length is invalid")
	}
	value := make([]byte, int(size))
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, err
	}
	return value, nil
}

func volcConnectionEvent(event int32) bool {
	switch event {
	case volcEventStartConnection, volcEventFinishConnection,
		volcEventConnectionStarted, volcEventConnectionFailed,
		volcEventConnectionFinished:
		return true
	default:
		return false
	}
}

func volcDownstreamConnectionEvent(event int32) bool {
	switch event {
	case volcEventConnectionStarted, volcEventConnectionFailed,
		volcEventConnectionFinished:
		return true
	default:
		return false
	}
}
