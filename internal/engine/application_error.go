package engine

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"net/http"
	"strings"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/l10n/translation"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/rs/zerolog/log"
)

func (e *Engine) moduleResponse(wire abi.Response, body []byte, mod *module.LoadedModule, locale string) EngineResponse {
	response := EngineResponse{StatusCode: wire.StatusCode, Headers: wire.Headers, Body: body}
	if len(body) == 0 && wire.StatusCode < http.StatusBadRequest && wire.ErrorKind == "" {
		return response
	}

	var envelope struct {
		Error jsontext.Value            `json:"error"`
		Other map[string]jsontext.Value `json:",embed"`
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
		Code  string                    `json:"code"`
		Other map[string]jsontext.Value `json:",embed"`
	}
	codeErr := json.Unmarshal(envelope.Error, &errorBody)
	code := errorBody.Code

	if decodeErr == nil && codeErr == nil && code != "" {
		if status := abi.FrameworkErrorStatus(code); wire.ErrorKind == abi.FrameworkError && status != 0 && status == wire.StatusCode {
			return response
		}

		if _, declared := mod.ErrorCodes[code]; declared && wire.ErrorKind == "" {
			response.StatusCode = abi.ApplicationErrorStatus(code, wire.StatusCode)
			if e.moduleRegistry == nil {
				return response
			}

			suffix, owned := strings.CutPrefix(code, mod.Manifest.Name+".")
			if !owned {
				return response
			}
			message, found := translation.Resolve(e.moduleRegistry.Translations.Lookup, mod.Manifest.Name, abi.TranslationInput{
				Key:    "errors." + suffix,
				Locale: locale,
			})
			if !found {
				return response
			}

			encodedMessage, err := json.Marshal(message)
			if err != nil {
				return response
			}
			if errorBody.Other == nil {
				errorBody.Other = make(map[string]jsontext.Value)
			}
			errorBody.Other["message"] = encodedMessage
			envelope.Error, err = json.Marshal(errorBody)
			if err != nil {
				return response
			}
			translatedBody, err := json.Marshal(envelope, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
			if err != nil {
				return response
			}
			response.Body = translatedBody
			response.Headers = maps.Clone(wire.Headers)
			maps.DeleteFunc(response.Headers, func(name, _ string) bool { return strings.EqualFold(name, "Content-Length") })
			return response
		}
	}

	log.Warn().Str("module", mod.Manifest.Name).Str("code", code).Msg("module returned an undeclared or invalid application error")

	return EngineResponse{
		StatusCode: http.StatusInternalServerError,
		Body:       []byte(`{"error":{"code":"internal_error","message":"An internal error occurred"}}`),
	}
}
