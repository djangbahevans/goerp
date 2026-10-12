// Package localefixture declares invocation locale probes.
package localefixture

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	eventdef "github.com/djangbahevans/goerp/sdk/go/events/def"
	jobdef "github.com/djangbahevans/goerp/sdk/go/jobs/def"
)

var Received = eventdef.Define[abi.LocaleInfo]("localeprobe.locale.received", eventdef.Description("Locale probe"))
var Process = jobdef.Define[abi.LocaleInfo]("check_locale", jobdef.Label("Check locale"))
