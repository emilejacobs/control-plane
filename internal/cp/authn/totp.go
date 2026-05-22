package authn

import (
	"crypto/rand"
	"encoding/base32"
	"net/url"
	"strconv"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTP parameters per RFC 6238 / ADR-010. totpSkew tolerates ±1 period of
// clock drift between the authenticator app and the Control Plane.
const (
	totpPeriod = 30
	totpSkew   = 1
	totpIssuer = "uKnomi"
)

// totpValidateOpts is the shared parameter set for code generation and
// validation, so the two never disagree.
var totpValidateOpts = totp.ValidateOpts{
	Period:    totpPeriod,
	Skew:      totpSkew,
	Digits:    otp.DigitsSix,
	Algorithm: otp.AlgorithmSHA1,
}

// newTotpSecret returns a fresh base32-encoded TOTP shared secret.
func newTotpSecret() string {
	var b [20]byte
	_, _ = rand.Read(b[:])
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
}

// totpCodeAt returns the 6-digit TOTP code for secret at time t. Tests use it
// to stand in for an authenticator app.
func totpCodeAt(secret string, t time.Time) (string, error) {
	return totp.GenerateCodeCustom(secret, t, totpValidateOpts)
}

// validateTotp reports whether code is a valid TOTP for secret at time t,
// allowing ±1 period of clock drift.
func validateTotp(secret, code string, t time.Time) bool {
	ok, _ := totp.ValidateCustom(code, secret, t, totpValidateOpts)
	return ok
}

// totpProvisioningURI builds the otpauth:// URI an authenticator app renders
// as a QR code, labelled "uKnomi:<account>".
func totpProvisioningURI(secret, account string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", strconv.Itoa(totpPeriod))
	u := url.URL{
		Scheme:   "otpauth",
		Host:     "totp",
		Path:     "/" + totpIssuer + ":" + account,
		RawQuery: q.Encode(),
	}
	return u.String()
}
