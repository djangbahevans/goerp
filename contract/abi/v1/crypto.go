package abi

// CryptoVerifyHMACInput is the request of host.crypto.verify_hmac. Algo is
// "sha256" or "sha512"; Sig is the raw MAC bytes, not their hex encoding.
type CryptoVerifyHMACInput struct {
	Algo string `msgpack:"algo"`
	Key  []byte `msgpack:"key"`
	Data []byte `msgpack:"data"`
	Sig  []byte `msgpack:"sig"`
}

// CryptoVerifyHMACOutput is the response of host.crypto.verify_hmac.
type CryptoVerifyHMACOutput struct {
	Valid bool `msgpack:"valid"`
}
