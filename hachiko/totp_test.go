package main

import (
	"encoding/base32"
	"testing"
	"time"
)

// RFC 6238's own vectors for HMAC-SHA1, which is what every authenticator does by default.
// The RFC prints eight digits; six is what an authenticator shows and what is checked here,
// so these are the last six of each published value.
func TestTOTPAgreesWithRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")

	for _, tc := range []struct {
		at   int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		step := tc.at / 30
		equal(t, totpAt(secret, step), tc.want, "the code at the step covering unix time")

		// And the same code verifies at that moment, which is the direction this is used in.
		matched, ok := totpVerify(secret, tc.want, time.Unix(tc.at, 0))
		equal(t, ok, true, "whether the published code verifies")
		equal(t, matched, step, "the step it verified at")
	}
}

// One step either side, which is the drift RFC 6238 suggests allowing and enough for a code
// typed into a phone a moment late. Two steps is not.
func TestACodeVerifiesOneStepEitherSideAndNoFurther(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	step := now.Unix() / 30

	for _, drift := range []int64{-1, 0, 1} {
		_, ok := totpVerify(secret, totpAt(secret, step+drift), now)
		equal(t, ok, true, "whether a code one step away verifies")
	}
	for _, drift := range []int64{-2, 2, 100} {
		_, ok := totpVerify(secret, totpAt(secret, step+drift), now)
		equal(t, ok, false, "whether a code further away verifies")
	}

	// Nothing that is not six digits is a code at all, and the right code for the wrong
	// secret is not one either.
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "050472"} {
		_, ok := totpVerify(secret, bad, now)
		equal(t, ok, false, "whether "+bad+" verifies")
	}

	// Whitespace around it is the shape a code pasted off a phone has.
	_, ok := totpVerify(secret, " 050471 ", now)
	equal(t, ok, true, "whether a code with spaces around it verifies")
}

// Which of the two forms `op read` answers with for a one-time-password field is 1Password's
// business, so both are read — and a URI asking for something this does not implement is
// refused rather than checked against the wrong algorithm.
func TestTheApprovalSecretIsReadFromEitherFormOpGivesBack(t *testing.T) {
	want := []byte("12345678901234567890")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(want)

	for _, value := range []string{
		encoded,
		"  " + encoded + "  ",
		base32.StdEncoding.EncodeToString(want),
		"otpauth://totp/hachiko:approval?secret=" + encoded + "&issuer=hachiko",
		"otpauth://totp/hachiko?secret=" + encoded + "&algorithm=SHA1&digits=6&period=30",
	} {
		secret, err := totpSecret(value)
		if err != nil {
			t.Fatalf("%s was refused: %v", value, err)
		}
		equal(t, string(secret), string(want), "the secret read from "+value)
	}

	for _, bad := range []string{
		"",
		"   ",
		"not base32 at all!",
		"otpauth://totp/x?secret=" + encoded + "&algorithm=SHA256",
		"otpauth://totp/x?secret=" + encoded + "&digits=8",
		"otpauth://totp/x?secret=" + encoded + "&period=60",
		"otpauth://totp/x?issuer=hachiko",
	} {
		if _, err := totpSecret(bad); err == nil {
			t.Errorf("%q was accepted as an approval secret", bad)
		}
	}
}

// An authenticator prints a secret in groups and in lower case, and neither changes it.
func TestASecretIsReadHoweverItWasCopiedOut(t *testing.T) {
	want := []byte("12345678901234567890")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(want)

	spaced := ""
	for i, r := range encoded {
		if i > 0 && i%4 == 0 {
			spaced += " "
		}
		spaced += string(r)
	}

	for _, value := range []string{spaced, lower(encoded), lower(spaced)} {
		secret, err := totpSecret(value)
		if err != nil {
			t.Fatalf("%q was refused: %v", value, err)
		}
		equal(t, string(secret), string(want), "the secret read from "+value)
	}
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}
