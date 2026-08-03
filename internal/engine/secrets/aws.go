package secrets

import "errors"

var ErrAwsNotSupported = errors.New("aws backend not yet supported")

type AwsConfig struct {
	AwsRegion string `env:"GOERP_AWS_REGION"`
}

func newAwsBackend() (Backend, error) {
	return nil, ErrAwsNotSupported
}
