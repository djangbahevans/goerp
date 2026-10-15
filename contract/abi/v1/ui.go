package abi

// UIPushInput sends a msgpack-encoded instruction array to a tenant UI channel.
type UIPushInput struct {
	Channel      string `msgpack:"channel"`
	Instructions []byte `msgpack:"instructions"`
}

// UIPushOutput counts connections that received the UI instructions.
type UIPushOutput struct {
	DeliveredCount int `msgpack:"delivered_count"`
}

const (
	ErrCodeUIChannelInvalid         = "ui.channel_invalid"
	ErrCodeUIInstructionNotPushable = "ui.instruction_not_pushable"
	ErrCodeUIPayloadTooLarge        = "ui.payload_too_large"
)
