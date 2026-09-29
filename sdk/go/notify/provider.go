package notify

import abi "github.com/djangbahevans/goerp/contract/abi/v1"

// Delivery job types an SMS or push provider connector handles
// (connector-guide.md §8, §9). The engine dispatches them to the tenant's
// active provider for the category; the connector registers a handler with
// engine.OnJob and declares provides.sms_provider or provides.push_provider,
// never a job_types entry:
//
//	engine.OnJob(notify.JobTypeSMSSend, func(ctx *engine.JobContext, p notify.SMSSendPayload) error {
//		...
//	})
//
// A handler that returns nil marks its deliveries accepted; an error leaves
// them retrying until the last attempt fails them, and jobs.PermanentError
// fails them at once.
const (
	JobTypeSMSSend  = abi.JobTypeSMSSend
	JobTypePushSend = abi.JobTypePushSend
)

// SMSSendPayload is an sms_send job's payload: one rendered SMS to one E.164
// phone number, from the tenant's sender ID (empty for the provider's
// default).
type SMSSendPayload = abi.SMSSendPayload

// PushSendPayload is a push_send job's payload: one rendered push
// notification to each of a user's device tokens.
type PushSendPayload = abi.PushSendPayload

// PushDeviceToken is one device a push_send job delivers to.
type PushDeviceToken = abi.PushDeviceToken
