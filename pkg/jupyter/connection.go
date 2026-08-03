package jupyter

import (
	"encoding/json"
	"fmt"
	"os"
)

// ConnectionInfo represents the structure of the JSON connection file provided by Jupyter.
type ConnectionInfo struct {
	ControlPort     int    `json:"control_port"`
	ShellPort       int    `json:"shell_port"`
	Transport       string `json:"transport"`
	SignatureScheme string `json:"signature_scheme"`
	StdinPort       int    `json:"stdin_port"`
	HbPort          int    `json:"hb_port"`
	IOPubPort       int    `json:"iopub_port"`
	IP              string `json:"ip"`
	Key             string `json:"key"`
}

// ReadConnectionFile reads and decodes a JSON connection file.
func ReadConnectionFile(path string) (*ConnectionInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read connection file %s: %w", path, err)
	}

	var conn ConnectionInfo
	if err := json.Unmarshal(data, &conn); err != nil {
		return nil, fmt.Errorf("failed to decode connection JSON: %w", err)
	}

	return &conn, nil
}
