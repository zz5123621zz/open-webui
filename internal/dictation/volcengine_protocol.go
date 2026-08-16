package dictation

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	volcProtocolVersion = 0x1
	volcHeaderWords     = 0x1

	volcMsgFullClientRequest  = 0x1
	volcMsgAudioOnlyClient    = 0x2
	volcMsgFullServerResponse = 0x9
	volcMsgError              = 0xf

	volcFlagNoSequence       = 0x0
	volcFlagPositiveSequence = 0x1
	volcFlagLastNoSequence   = 0x2
	volcFlagLastSequence     = 0x3

	volcSerializationRaw  = 0x0
	volcSerializationJSON = 0x1

	volcCompressionNone = 0x0
	volcCompressionGzip = 0x1

	maxVolcDictationFrameBytes = 2 * 1024 * 1024
	maxVolcAudioFrameBytes     = 64 * 1024
)

type volcMessage struct {
	MessageType   uint8
	Flag          uint8
	Serialization uint8
	Compression   uint8
	Sequence      int32
	ErrorCode     uint32
	Last          bool
	Payload       []byte
}

func marshalVolcFullRequest(payload []byte) ([]byte, error) {
	return marshalVolcClientMessage(volcMessage{
		MessageType:   volcMsgFullClientRequest,
		Flag:          volcFlagPositiveSequence,
		Serialization: volcSerializationJSON,
		Compression:   volcCompressionGzip,
		Sequence:      1,
		Payload:       payload,
	})
}

func marshalVolcAudio(sequence int32, payload []byte, last bool) ([]byte, error) {
	if sequence < 2 {
		return nil, errors.New("Volcengine dictation audio sequence is invalid")
	}
	if len(payload) == 0 || len(payload) > maxVolcAudioFrameBytes {
		return nil, errors.New("Volcengine dictation audio frame size is invalid")
	}
	flag := uint8(volcFlagPositiveSequence)
	if last {
		flag = volcFlagLastSequence
		sequence = -sequence
	}
	return marshalVolcClientMessage(volcMessage{
		MessageType:   volcMsgAudioOnlyClient,
		Flag:          flag,
		Serialization: volcSerializationRaw,
		Compression:   volcCompressionGzip,
		Sequence:      sequence,
		Payload:       payload,
	})
}

func marshalVolcClientMessage(message volcMessage) ([]byte, error) {
	if len(message.Payload) > maxVolcDictationFrameBytes {
		return nil, errors.New("Volcengine dictation payload is too large")
	}
	payload := message.Payload
	var err error
	if message.Compression == volcCompressionGzip {
		payload, err = gzipVolcPayload(payload)
		if err != nil {
			return nil, err
		}
	}
	var buffer bytes.Buffer
	buffer.WriteByte(byte(volcProtocolVersion<<4 | volcHeaderWords))
	buffer.WriteByte(byte(message.MessageType<<4 | message.Flag))
	buffer.WriteByte(byte(message.Serialization<<4 | message.Compression))
	buffer.WriteByte(0)
	if message.Flag&0x1 != 0 {
		if err := binary.Write(&buffer, binary.BigEndian, message.Sequence); err != nil {
			return nil, err
		}
	}
	if err := binary.Write(&buffer, binary.BigEndian, uint32(len(payload))); err != nil {
		return nil, err
	}
	_, err = buffer.Write(payload)
	return buffer.Bytes(), err
}

func unmarshalVolcResponse(data []byte) (volcMessage, error) {
	if len(data) < 4 {
		return volcMessage{}, errors.New(
			"Volcengine dictation frame is shorter than its header",
		)
	}
	version := data[0] >> 4
	headerWords := data[0] & 0x0f
	if version != volcProtocolVersion || headerWords < 1 {
		return volcMessage{}, fmt.Errorf(
			"unsupported Volcengine dictation protocol header (%d/%d)",
			version, headerWords,
		)
	}
	headerBytes := int(headerWords) * 4
	if headerBytes > len(data) {
		return volcMessage{}, errors.New(
			"Volcengine dictation frame has an invalid header size",
		)
	}
	message := volcMessage{
		MessageType:   data[1] >> 4,
		Flag:          data[1] & 0x0f,
		Serialization: data[2] >> 4,
		Compression:   data[2] & 0x0f,
	}
	message.Last = message.Flag&0x2 != 0
	reader := bytes.NewReader(data[headerBytes:])
	if message.Flag&0x1 != 0 {
		if err := binary.Read(reader, binary.BigEndian, &message.Sequence); err != nil {
			return volcMessage{}, fmt.Errorf(
				"decode Volcengine dictation sequence: %w", err,
			)
		}
	}
	switch message.MessageType {
	case volcMsgFullServerResponse:
	case volcMsgError:
		if err := binary.Read(reader, binary.BigEndian, &message.ErrorCode); err != nil {
			return volcMessage{}, fmt.Errorf(
				"decode Volcengine dictation error code: %w", err,
			)
		}
	default:
		return volcMessage{}, fmt.Errorf(
			"unexpected Volcengine dictation message type %d",
			message.MessageType,
		)
	}
	var payloadSize uint32
	if err := binary.Read(reader, binary.BigEndian, &payloadSize); err != nil {
		return volcMessage{}, fmt.Errorf(
			"decode Volcengine dictation payload size: %w", err,
		)
	}
	if payloadSize > maxVolcDictationFrameBytes ||
		uint64(payloadSize) != uint64(reader.Len()) {
		return volcMessage{}, errors.New(
			"Volcengine dictation payload length is invalid",
		)
	}
	message.Payload = make([]byte, int(payloadSize))
	if _, err := io.ReadFull(reader, message.Payload); err != nil {
		return volcMessage{}, err
	}
	switch message.Compression {
	case volcCompressionNone:
	case volcCompressionGzip:
		payload, err := gunzipVolcPayload(message.Payload)
		if err != nil {
			return volcMessage{}, err
		}
		message.Payload = payload
	default:
		return volcMessage{}, errors.New(
			"unsupported Volcengine dictation compression",
		)
	}
	return message, nil
}

func gzipVolcPayload(payload []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(payload); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > maxVolcDictationFrameBytes {
		return nil, errors.New("Volcengine dictation gzip payload is too large")
	}
	return buffer.Bytes(), nil
}

func gunzipVolcPayload(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("open Volcengine dictation gzip payload: %w", err)
	}
	decompressed, readErr := io.ReadAll(io.LimitReader(
		reader, maxVolcDictationFrameBytes+1,
	))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read Volcengine dictation gzip payload: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close Volcengine dictation gzip payload: %w", closeErr)
	}
	if len(decompressed) > maxVolcDictationFrameBytes {
		return nil, errors.New(
			"Volcengine dictation decompressed payload is too large",
		)
	}
	return decompressed, nil
}
