package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"time"
)

// SwitchSocket is the socket of the root helper that moves traffic. Any local user may connect; the helper learns who it
// is talking to from the kernel and serves only the users of managed projects, and only for their own sites.
const SwitchSocket = "/run/lite-deploy/switch.sock"

// SwitchRequest is the one line a project user sends to the helper.
type SwitchRequest struct {
	Slot string `json:"slot"`
}

// SwitchReply is the answer.
type SwitchReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// HelperSwitcher returns a function that asks the helper on the socket to switch traffic to a slot.
func HelperSwitcher(sock string) func(ctx context.Context, slot string) error {
	return func(ctx context.Context, slot string) error {
		d := net.Dialer{Timeout: 5 * time.Second}
		conn, err := d.DialContext(ctx, "unix", sock)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such file") {
				return ErrNoHelper
			}
			return err
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Minute))
		b, _ := json.Marshal(SwitchRequest{Slot: slot})
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return err
		}
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil && len(line) == 0 {
			return errors.New("the helper did not answer: " + err.Error())
		}
		var rep SwitchReply
		if json.Unmarshal(line, &rep) != nil {
			return errors.New("the helper answered something unreadable")
		}
		if !rep.OK {
			return errors.New(rep.Error)
		}
		return nil
	}
}
