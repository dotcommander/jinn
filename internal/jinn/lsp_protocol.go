package jinn

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// lspRPCMsg is the wire shape for JSON-RPC 2.0 requests and responses.
type lspRPCMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *lspRPCError    `json:"error,omitempty"`
}

type lspRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// maxLSPFrame caps a single server response frame. A hostile or buggy server
// could send an enormous Content-Length and force an OOM allocation; reject
// frames above this bound before allocating.
const maxLSPFrame = 64 << 20 // 64 MB

// sendRequest sends a JSON-RPC request and reads the matching reply.
// Relies on the LSP server returning responses in order for the synchronous
// request sequence used here (initialize → didOpen → query → shutdown).
func (c *lspClient) sendRequest(method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	msg := lspRPCMsg{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
	if err := c.writeMsg(msg); err != nil {
		return nil, err
	}
	return c.readReply(id)
}

// sendNotification sends a JSON-RPC notification (no id, no reply expected).
func (c *lspClient) sendNotification(method string, params any) error {
	return c.writeMsg(lspRPCMsg{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *lspClient) writeMsg(msg lspRPCMsg) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("lsp marshal: %w", err)
	}
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

// readReply reads frames until it finds one whose id matches wantID.
// Notifications and out-of-order messages from the server are discarded.
func (c *lspClient) readReply(wantID int64) (json.RawMessage, error) {
	for {
		frame, err := c.readFrame()
		if err != nil {
			return nil, fmt.Errorf("lsp read: %w", err)
		}
		var reply lspRPCMsg
		if err := json.Unmarshal(frame, &reply); err != nil {
			return nil, fmt.Errorf("lsp unmarshal: %w", err)
		}
		if reply.ID == nil || *reply.ID != wantID {
			continue // server notification or different id — skip
		}
		if reply.Error != nil {
			return nil, fmt.Errorf("lsp error %d: %s", reply.Error.Code, reply.Error.Message)
		}
		return reply.Result, nil
	}
}

// readFrame reads one Content-Length framed LSP message from stdout.
func (c *lspClient) readFrame() ([]byte, error) {
	contentLen := -1
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("lsp header read: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if strings.HasPrefix(line, "Content-Length:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			if err != nil {
				return nil, fmt.Errorf("lsp bad Content-Length: %w", err)
			}
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, errors.New("lsp: missing Content-Length header")
	}
	if contentLen > maxLSPFrame {
		return nil, fmt.Errorf("lsp: Content-Length %d exceeds max frame size %d", contentLen, maxLSPFrame)
	}
	buf := make([]byte, contentLen)
	if _, err := io.ReadFull(c.stdout, buf); err != nil {
		return nil, fmt.Errorf("lsp body read: %w", err)
	}
	return buf, nil
}
