package manifest

import (
	"regexp"
	"strconv"

	"github.com/Masterminds/semver/v3"
	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	enTranslations "github.com/go-playground/validator/v10/translations/en"
	"github.com/rs/zerolog/log"
)

// ABIVersions is the set of ABI versions this engine build supports. Not yet
// wired into validation below: abi_version is currently checked for shape
// only (a bare non-negative integer string). Once engine negotiation exists
// (host-abi-reference.md §25), abi_version should also be checked against
// this set so unsupported versions are rejected at manifest-load time,
// not just at module-load time.
// TODO: Find a better home for this: the manifest package shouldn't own
// the engine's supported-ABI-version list.
var ABIVersions = []string{"1", "2"}

var validNameRegex = regexp.MustCompile("^[a-z][a-z0-9_]{0,63}$")
var validABIVersionRegex = regexp.MustCompile(`^[0-9]+$`)

func nameRegex(fl validator.FieldLevel) bool {
	return validNameRegex.MatchString(fl.Field().String())
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

func validateManifest(m Manifest) error {
	english := en.New()
	uni := ut.New(english, english)
	trans, _ := uni.GetTranslator("en")

	validate := validator.New(validator.WithRequiredStructEnabled())
	enTranslations.RegisterDefaultTranslations(validate, trans)

	if err := validate.RegisterValidation("name_regex", nameRegex); err != nil {
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

	return validate.Struct(m)
}
