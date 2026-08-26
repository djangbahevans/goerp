package manifest

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	enTranslations "github.com/go-playground/validator/v10/translations/en"
	"github.com/rs/zerolog/log"
)

var validNameRegex = regexp.MustCompile("^[a-z][a-z0-9_]{0,63}$")
var validABIVersionRegex = regexp.MustCompile(`^[0-9]+$`)
var validSha256Regex = regexp.MustCompile("^[a-fA-F0-9]{64}$")
var validEventNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

func nameRegex(fl validator.FieldLevel) bool {
	return validNameRegex.MatchString(fl.Field().String())
}

func eventName(fl validator.FieldLevel) bool {
	return validEventNameRegex.MatchString(fl.Field().String())
}

func abiVersion(fl validator.FieldLevel) bool {
	return validABIVersionRegex.MatchString(fl.Field().String())
}

func maxWarn(fl validator.FieldLevel) bool {
	val := fl.Field().String()
	param := fl.Param()
	field := fl.StructFieldName()

	threshold, err := strconv.Atoi(param)
	if err != nil {
		threshold = 4096
	}

	if len(val) > threshold {
		log.Warn().Msgf("field '%s' length (%d) exceeds dynamic threshold of %d characters", field, len(val), threshold)
	}

	return true
}

func versionRange(fl validator.FieldLevel) bool {
	rangeConstraintStr := fl.Field().String()

	_, err := semver.NewConstraint(rangeConstraintStr)
	return err == nil
}

func sha256Checksum(fl validator.FieldLevel) bool {
	val := fl.Field().String()

	hash, found := strings.CutPrefix(val, "sha256:")
	if !found {
		return false
	}

	return validSha256Regex.MatchString(hash)
}

func registerSimpleTranslation(v *validator.Validate, trans ut.Translator, tag, translation string) error {
	return v.RegisterTranslation(tag, trans,
		func(ut ut.Translator) error {
			return ut.Add(tag, translation, true)
		},
		func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T(tag, fe.Field())
			return t
		},
	)
}

func validateManifest(m Manifest) error {
	english := en.New()
	uni := ut.New(english, english)
	trans, _ := uni.GetTranslator("en")

	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := enTranslations.RegisterDefaultTranslations(validate, trans); err != nil {
		return err
	}

	if err := validate.RegisterValidation("name_regex", nameRegex); err != nil {
		return err
	}

	if err := validate.RegisterValidation("event_name", eventName); err != nil {
		return err
	}

	if err := validate.RegisterValidation("max_warn", maxWarn); err != nil {
		return err
	}

	if err := validate.RegisterValidation("version_range", versionRange); err != nil {
		return err
	}

	if err := validate.RegisterValidation("abi_version", abiVersion); err != nil {
		return err
	}

	if err := validate.RegisterValidation("sha256_checksum", sha256Checksum); err != nil {
		return err
	}

	customTranslations := map[string]string{
		"name_regex":                   "{0} must be lowercase alphanumeric/underscore, starting with a letter (max 64 chars)",
		"event_name":                   "{0} must follow the module.noun.verb naming convention (lowercase, dot-separated)",
		"semver":                       "{0} must be a valid semver version",
		"version_range":                "{0} must be a valid semver range",
		"abi_version":                  "{0} must be a non-negative integer string",
		"sha256_checksum":              "{0} must be a sha256:-prefixed 64-character hex hash",
		"required_with_workflow_types": "{0} is required when workflow_types is not empty",
		"required_with_http_fetch":     "{0} must be non-empty when the http.fetch capability is declared",
		"required_for_emits":           "{0} must include the event.emit capability when emits is not empty",
	}
	for tag, translation := range customTranslations {
		if err := registerSimpleTranslation(validate, trans, tag, translation); err != nil {
			return err
		}
	}

	validate.RegisterStructValidation(func(sl validator.StructLevel) {
		m := sl.Current().Interface().(Manifest)
		if len(m.WorkflowTypes) > 0 && m.WorkerChecksum == "" {
			sl.ReportError(m.WorkerChecksum,
				"WorkerChecksum",
				"worker_checksum",
				"required_with_workflow_types",
				"",
			)
		}

		if slices.Contains(m.Capabilities, "http.fetch") && len(m.HTTPAllowlist) == 0 {
			sl.ReportError(m.HTTPAllowlist,
				"HTTPAllowlist",
				"http_allowlist",
				"required_with_http_fetch",
				"",
			)
		}

		if len(m.Emits) > 0 && !slices.Contains(m.Capabilities, "event.emit") {
			sl.ReportError(m.Capabilities,
				"Capabilities",
				"capabilities",
				"required_for_emits",
				"",
			)
		}
	}, Manifest{})

	var msgs []string

	if err := validate.Struct(m); err != nil {
		var verrs validator.ValidationErrors
		if !errors.As(err, &verrs) {
			return err
		}
		for _, fe := range verrs {
			msgs = append(msgs, fe.Translate(trans))
		}
	}

	if err := validateModuleType(m); err != nil {
		msgs = append(msgs, err.Error())
	}

	if err := validatePolicies(m); err != nil {
		msgs = append(msgs, err.Error())
	}

	if err := validateNotificationTypes(m); err != nil {
		msgs = append(msgs, err.Error())
	}

	if len(msgs) == 0 {
		return nil
	}

	return errors.New(strings.Join(msgs, "; "))
}
