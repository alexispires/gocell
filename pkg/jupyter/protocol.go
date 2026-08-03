package jupyter

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-zeromq/zmq4"
)

const (
	Delim = "<IDS|MSG>"
)

// Header defines the standard header of a Jupyter message.
type Header struct {
	MsgID    string `json:"msg_id"`
	Username string `json:"username"`
	Session  string `json:"session"`
	MsgType  string `json:"msg_type"`
	Version  string `json:"version"`
	Date     string `json:"date,omitempty"`
}

// NewHeader creates a new message header.
func NewHeader(msgType, session string) Header {
	return Header{
		MsgID:    fmt.Sprintf("%d", time.Now().UnixNano()),
		Username: "gosk",
		Session:  session,
		MsgType:  msgType,
		Version:  "5.3",
		Date:     time.Now().UTC().Format(time.RFC3339),
	}
}

// Message represents a fully decoded Jupyter protocol message.
type Message struct {
	Identities   [][]byte
	Header       Header
	ParentHeader Header
	Metadata     map[string]any
	Content      json.RawMessage
	RawHeader    []byte
	RawParent    []byte
	RawMeta      []byte
	RawContent   []byte
}

// DecodeMessage decodes a set of ZMQ frames into a Message struct.
func DecodeMessage(msg zmq4.Msg, key []byte) (*Message, error) {
	frames := msg.Frames
	delimIdx := -1
	for i, frame := range frames {
		if string(frame) == Delim {
			delimIdx = i
			break
		}
	}

	if delimIdx == -1 || len(frames) < delimIdx+5 {
		return nil, fmt.Errorf("invalid Jupyter message format: delimiter not found or not enough frames")
	}

	identities := frames[:delimIdx]
	sig := string(frames[delimIdx+1])
	rawHeader := frames[delimIdx+2]
	rawParent := frames[delimIdx+3]
	rawMeta := frames[delimIdx+4]
	rawContent := frames[delimIdx+5]

	if !ValidateHMAC(key, sig, rawHeader, rawParent, rawMeta, rawContent) {
		return nil, fmt.Errorf("invalid HMAC signature")
	}

	var header Header
	if err := json.Unmarshal(rawHeader, &header); err != nil {
		return nil, fmt.Errorf("failed to decode header: %w", err)
	}

	var parentHeader Header
	if len(rawParent) > 0 && string(rawParent) != "{}" {
		_ = json.Unmarshal(rawParent, &parentHeader)
	}

	var metadata map[string]any
	if len(rawMeta) > 0 {
		_ = json.Unmarshal(rawMeta, &metadata)
	}

	return &Message{
		Identities:   identities,
		Header:       header,
		ParentHeader: parentHeader,
		Metadata:     metadata,
		Content:      rawContent,
		RawHeader:    rawHeader,
		RawParent:    rawParent,
		RawMeta:      rawMeta,
		RawContent:   rawContent,
	}, nil
}

// EncodeMessage encodes a Jupyter message into zmq4.Msg frames ready to send.
func EncodeMessage(msg *Message, key []byte) (zmq4.Msg, error) {
	rawHeader, err := json.Marshal(msg.Header)
	if err != nil {
		return zmq4.Msg{}, err
	}

	rawParent := []byte("{}")
	if msg.ParentHeader.MsgID != "" {
		if rp, errP := json.Marshal(msg.ParentHeader); errP == nil {
			rawParent = rp
		}
	}

	rawMeta := []byte("{}")
	if msg.Metadata != nil {
		if rm, errM := json.Marshal(msg.Metadata); errM == nil {
			rawMeta = rm
		}
	}

	rawContent := msg.Content
	if len(rawContent) == 0 {
		rawContent = []byte("{}")
	}

	sig := SignHMAC(key, rawHeader, rawParent, rawMeta, rawContent)

	frames := make([][]byte, 0, len(msg.Identities)+6)
	frames = append(frames, msg.Identities...)
	frames = append(frames, []byte(Delim))
	frames = append(frames, []byte(sig))
	frames = append(frames, rawHeader)
	frames = append(frames, rawParent)
	frames = append(frames, rawMeta)
	frames = append(frames, rawContent)

	return zmq4.NewMsgFrom(frames...), nil
}
