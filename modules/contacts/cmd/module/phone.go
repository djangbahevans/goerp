package main

import (
	_ "embed"
	"encoding/json/v2"
	"strings"
	"unicode"

	"github.com/djangbahevans/goerp/sdk/go/config"
)

// Calling codes follow the libphonenumber-js metadata used by the shell phone input.
//
//go:embed calling_codes.json
var callingCodesJSON []byte

var callingCodes = func() map[string]string {
	var codes map[string]string
	if err := json.Unmarshal(callingCodesJSON, &codes); err != nil {
		panic(err)
	}
	return codes
}()

func phoneDigits(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)
}

func phoneSearchValues(query string) []string {
	if strings.ContainsFunc(query, func(r rune) bool {
		return (r < '0' || r > '9') && !unicode.IsSpace(r) && !strings.ContainsRune("+()-./", r)
	}) {
		return nil
	}
	digits := phoneDigits(query)
	if digits == "" {
		return nil
	}
	values := []string{digits}
	code := callingCodes[strings.ToUpper(config.CompanyCountry.Get())]
	if code == "" {
		return values
	}
	if national, ok := strings.CutPrefix(digits, "0"); ok && national != "" {
		values = append(values, code+national)
	} else if national, ok := strings.CutPrefix(digits, code); ok && national != "" {
		values = append(values, "0"+national)
	}
	return values
}
