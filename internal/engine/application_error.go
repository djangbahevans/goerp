package engine

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/rs/zerolog/log"
)

func moduleResponse(wire abi.Response, body []byte, mod *module.LoadedModule) EngineResponse {
	response := EngineResponse{StatusCode: wire.StatusCode, Headers: wire.Headers, Body: body}
	if len(body) == 0 && wire.StatusCode < http.StatusBadRequest && wire.ErrorKind == "" {
		return response
	}

	var envelope struct {
		Error jsontext.Value `json:"error"`
	}
	decodeErr := json.Unmarshal(body, &envelope)
	trimmed := bytes.TrimSpace(body)
	isObject := len(trimmed) > 0 && trimmed[0] == '{'
	if wire.StatusCode < http.StatusBadRequest && wire.ErrorKind == "" {
		if decodeErr != nil && !isObject {
			return response
		}

		if decodeErr == nil && (len(envelope.Error) == 0 || string(envelope.Error) == "null") {
			return response
		}
	}

	var errorBody struct {
		Code string `json:"code"`
	}
	codeErr := json.Unmarshal(envelope.Error, &errorBody)
	code := errorBody.Code

	if decodeErr == nil && codeErr == nil && code != "" {
		if status := abi.FrameworkErrorStatus(code); wire.ErrorKind == abi.FrameworkError && status != 0 && status == wire.StatusCode {
			return response
		}

		if _, declared := mod.ErrorCodes[code]; declared && wire.ErrorKind == "" {
			response.StatusCode = abi.ApplicationErrorStatus(code, wire.StatusCode)
			return response
		}
	}

	log.Warn().Str("module", mod.Manifest.Name).Str("code", code).Msg("module returned an undeclared or invalid application error")

	return EngineResponse{
		StatusCode: http.StatusInternalServerError,
		Body:       []byte(`{"error":{"code":"internal_error","message":"An internal error occurred"}}`),
	}
}
