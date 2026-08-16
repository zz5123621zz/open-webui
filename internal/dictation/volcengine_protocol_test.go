package dictation

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"
)

func TestMarshalVolcFullRequestUsesGzipJSONAndSequenceOne(t *testing.T) {
	payload := []byte(`{"request":{"model_name":"bigmodel"}}`)
	frame, err := marshalVolcFullRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	message, err := decodeVolcClientTestFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if message.MessageType != volcMsgFullClientRequest ||
		message.Flag != volcFlagPositiveSequence ||
		message.Serialization != volcSerializationJSON ||
		message.Compression != volcCompressionGzip ||
		message.Sequence != 1 ||
		!bytes.Equal(message.Payload, payload) {
		t.Fatalf("full request = %#v", message)
	}
}

func TestMarshalVolcAudioUsesPositiveAndFinalNegativeSequences(t *testing.T) {
	audio := []byte{1, 2, 3, 4}
	regular, err := marshalVolcAudio(2, audio, false)
	if err != nil {
		t.Fatal(err)
	}
	regularMessage, err := decodeVolcClientTestFrame(regular)
	if err != nil {
		t.Fatal(err)
	}
	if regularMessage.MessageType != volcMsgAudioOnlyClient ||
		regularMessage.Flag != volcFlagPositiveSequence ||
		regularMessage.Sequence != 2 ||
		!bytes.Equal(regularMessage.Payload, audio) {
		t.Fatalf("regular audio message = %#v", regularMessage)
	}

	final, err := marshalVolcAudio(3, audio, true)
	if err != nil {
		t.Fatal(err)
	}
	finalMessage, err := decodeVolcClientTestFrame(final)
	if err != nil {
		t.Fatal(err)
	}
	if finalMessage.Flag != volcFlagLastSequence ||
		finalMessage.Sequence != -3 ||
		!bytes.Equal(finalMessage.Payload, audio) {
		t.Fatalf("final audio message = %#v", finalMessage)
	}
}

func TestMarshalVolcAudioRejectsInvalidFrames(t *testing.T) {
	if _, err := marshalVolcAudio(1, []byte{1, 2}, false); err == nil {
		t.Fatal("sequence one audio error = nil")
	}
	if _, err := marshalVolcAudio(2, nil, false); err == nil {
		t.Fatal("empty audio error = nil")
	}
	if _, err := marshalVolcAudio(
		2,
		make([]byte, maxVolcAudioFrameBytes+1),
		false,
	); err == nil {
		t.Fatal("oversized audio error = nil")
	}
}

func TestUnmarshalVolcResponseDecodesCompressedResponseAndError(t *testing.T) {
	responsePayload := []byte(`{"result":{"text":"侬好"}}`)
	responseFrame := marshalVolcServerTestFrame(
		t,
		volcMsgFullServerResponse,
		volcFlagPositiveSequence,
		4,
		0,
		responsePayload,
		true,
	)
	response, err := unmarshalVolcResponse(responseFrame)
	if err != nil {
		t.Fatal(err)
	}
	if response.Sequence != 4 || response.Last ||
		response.Serialization != volcSerializationJSON ||
		!bytes.Equal(response.Payload, responsePayload) {
		t.Fatalf("response = %#v", response)
	}

	errorPayload := []byte(`{"error":"quota"}`)
	errorFrame := marshalVolcServerTestFrame(
		t,
		volcMsgError,
		volcFlagLastNoSequence,
		0,
		55000031,
		errorPayload,
		false,
	)
	providerError, err := unmarshalVolcResponse(errorFrame)
	if err != nil {
		t.Fatal(err)
	}
	if providerError.ErrorCode != 55000031 ||
		!providerError.Last ||
		!bytes.Equal(providerError.Payload, errorPayload) {
		t.Fatalf("provider error = %#v", providerError)
	}
}

func TestUnmarshalVolcResponseRejectsMalformedFrames(t *testing.T) {
	valid := marshalVolcServerTestFrame(
		t,
		volcMsgFullServerResponse,
		volcFlagPositiveSequence,
		1,
		0,
		[]byte(`{}`),
		false,
	)
	for name, frame := range map[string][]byte{
		"short header":      valid[:3],
		"truncated payload": valid[:len(valid)-1],
		"wrong version":     append([]byte{0x21}, valid[1:]...),
		"unknown type":      append([]byte{valid[0], 0x80}, valid[2:]...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := unmarshalVolcResponse(frame); err == nil {
				t.Fatal("unmarshal error = nil")
			}
		})
	}
}

func decodeVolcClientTestFrame(frame []byte) (volcMessage, error) {
	if len(frame) < 4 {
		return volcMessage{}, errors.New("short test frame")
	}
	message := volcMessage{
		MessageType:   frame[1] >> 4,
		Flag:          frame[1] & 0x0f,
		Serialization: frame[2] >> 4,
		Compression:   frame[2] & 0x0f,
	}
	headerBytes := int(frame[0]&0x0f) * 4
	reader := bytes.NewReader(frame[headerBytes:])
	if message.Flag&0x1 != 0 {
		if err := binary.Read(reader, binary.BigEndian, &message.Sequence); err != nil {
			return volcMessage{}, err
		}
	}
	var payloadSize uint32
	if err := binary.Read(reader, binary.BigEndian, &payloadSize); err != nil {
		return volcMessage{}, err
	}
	if uint64(payloadSize) != uint64(reader.Len()) {
		return volcMessage{}, errors.New("invalid test payload size")
	}
	message.Payload = make([]byte, payloadSize)
	if _, err := reader.Read(message.Payload); err != nil {
		return volcMessage{}, err
	}
	if message.Compression == volcCompressionGzip {
		payload, err := gunzipVolcPayload(message.Payload)
		if err != nil {
			return volcMessage{}, err
		}
		message.Payload = payload
	}
	return message, nil
}

func marshalVolcServerTestFrame(
	t *testing.T,
	messageType uint8,
	flag uint8,
	sequence int32,
	errorCode uint32,
	payload []byte,
	compressed bool,
) []byte {
	t.Helper()
	compression := uint8(volcCompressionNone)
	if compressed {
		var output bytes.Buffer
		writer := gzip.NewWriter(&output)
		if _, err := writer.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		payload = output.Bytes()
		compression = volcCompressionGzip
	}
	var frame bytes.Buffer
	frame.WriteByte(volcProtocolVersion<<4 | volcHeaderWords)
	frame.WriteByte(messageType<<4 | flag)
	frame.WriteByte(volcSerializationJSON<<4 | compression)
	frame.WriteByte(0)
	if flag&0x1 != 0 {
		if err := binary.Write(&frame, binary.BigEndian, sequence); err != nil {
			t.Fatal(err)
		}
	}
	if messageType == volcMsgError {
		if err := binary.Write(&frame, binary.BigEndian, errorCode); err != nil {
			t.Fatal(err)
		}
	}
	if err := binary.Write(
		&frame,
		binary.BigEndian,
		uint32(len(payload)),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := frame.Write(payload); err != nil {
		t.Fatal(err)
	}
	return frame.Bytes()
}

func TestVolcTestFrameHelperProducesJSON(t *testing.T) {
	// Keep the shared test helper honest: several provider tests depend on it
	// generating a payload that survives both gzip and JSON decoding.
	frame := marshalVolcServerTestFrame(
		t,
		volcMsgFullServerResponse,
		volcFlagLastSequence,
		-2,
		0,
		[]byte(`{"result":{"text":"test"}}`),
		true,
	)
	message, err := unmarshalVolcResponse(frame)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatal(err)
	}
}
