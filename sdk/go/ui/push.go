package ui

import (
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/djangbahevans/goerp/sdk/go/internal/notifydata"
	"github.com/vmihailenco/msgpack/v5"
)

// Push delivers Toast, Reload and SetBadge instructions to subscribed connections
// in the calling tenant and returns the connection count, including other engine
// instances. It requires ui.push. Offline users receive nothing and count as zero.
// Invalid channels, unsupported instructions and payloads above 64 KiB fail with
// ui.channel_invalid, ui.instruction_not_pushable and ui.payload_too_large.
// Delivery is ephemeral; a transport error can follow partial delivery.
func Push(channel string, instructions ...Instruction) (int, error) {
	data, err := encodePush(instructions)
	if err != nil {
		return 0, err
	}

	var out abi.UIPushOutput
	err = hostcall.Do(hostUIPush, abi.UIPushInput{
		Channel:      channel,
		Instructions: data,
	}, &out)
	return out.DeliveredCount, err
}

func encodePush(instructions []Instruction) ([]byte, error) {
	values := make([]map[string]any, len(instructions))
	for index, instruction := range instructions {
		if instruction.value == nil {
			continue
		}

		value, err := notifydata.Values(instruction)
		if err != nil {
			return nil, fmt.Errorf("encode UI instruction: %w", err)
		}
		values[index] = value
	}

	return msgpack.Marshal(values)
}

// UserChannel targets one user's subscribed connections in the calling tenant.
func UserChannel(userID string) string {
	return "ui:user:" + userID
}

// RoleChannel targets subscribers who currently hold the named role.
func RoleChannel(role string) string {
	return "ui:role:" + role
}

// RecordChannel targets subscribers who can read the resource's record.
func RecordChannel(resource, recordID string) string {
	return "ui:record:" + resource + ":" + recordID
}
