package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Why there is a second factor at all: a reply in Discord is whoever holds Tim's Discord
// account, and most of what he replies is harmless — pick option two, stop the worker,
// leave it alone. What is not harmless is the never list, and an account taken over
// should not be able to reach it. So an action on that list needs a code from the
// authenticator on his phone as well, checked here on the Mac against a secret the
// on-call agent is never given.
//
// RFC 6238 with the defaults every authenticator uses: SHA-1, thirty seconds, six
// digits, and one step either side for a code typed a moment late.
const (
	totpStep   = 30 * time.Second
	totpDigits = 6
	totpSkew   = 1
)

// The secret as 1Password hands it over. An OTP field holds an otpauth:// URI and a
// concealed field holds the bare base32 somebody pasted into it, and which of the two
// `op read` answers with is 1Password's decision rather than this one's — so both are
// read, and a URI asking for anything this does not implement is refused rather than
// verified against the wrong algorithm.
func totpSecret(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("there is no approval secret, so no code can be checked")
	}

	if strings.HasPrefix(strings.ToLower(value), "otpauth://") {
		uri, err := url.Parse(value)
		if err != nil {
			return nil, errors.New("the approval secret is not an otpauth URI this can read")
		}
		query := uri.Query()

		if algorithm := query.Get("algorithm"); algorithm != "" && !strings.EqualFold(algorithm, "SHA1") {
			return nil, fmt.Errorf("the approval secret asks for %s and this checks SHA1", algorithm)
		}
		if digits := query.Get("digits"); digits != "" && digits != fmt.Sprint(totpDigits) {
			return nil, fmt.Errorf("the approval secret asks for %s digits and this checks %d", digits, totpDigits)
		}
		if period := query.Get("period"); period != "" && period != "30" {
			return nil, fmt.Errorf("the approval secret asks for a %s second step and this checks 30", period)
		}
		value = query.Get("secret")
	}

	// Authenticators print a secret in groups and lower case, and neither changes it.
	value = strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(value))

	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(value, "="))
	if err != nil || len(secret) == 0 {
		return nil, errors.New("the approval secret is not base32, so no code can be checked")
	}
	return secret, nil
}

func totpAt(secret []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))

	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	return fmt.Sprintf("%0*d", totpDigits, truncated%1_000_000)
}

// The step a code belongs to, which is what makes one accepted once: a code is good for
// thirty seconds and would otherwise be good for all of them, and a reply nobody can
// take back is one somebody can replay.
func totpVerify(secret []byte, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}

	current := now.Unix() / int64(totpStep.Seconds())
	for drift := -totpSkew; drift <= totpSkew; drift++ {
		step := current + int64(drift)
		if subtle.ConstantTimeCompare([]byte(totpAt(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
