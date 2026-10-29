package gateway

import "fmt"

// CloseCode is why a websocket closed: RFC 6455's own codes, and Discord's 4000s.
type CloseCode int

const (
	CloseNormal        CloseCode = 1000
	CloseGoingAway     CloseCode = 1001
	CloseProtocolError CloseCode = 1002
	CloseNoStatus      CloseCode = 1005

	CloseUnknownError         CloseCode = 4000
	CloseUnknownOpcode        CloseCode = 4001
	CloseDecodeError          CloseCode = 4002
	CloseNotAuthenticated     CloseCode = 4003
	CloseAuthenticationFailed CloseCode = 4004
	CloseAlreadyAuthenticated CloseCode = 4005
	CloseInvalidSeq           CloseCode = 4007
	CloseRateLimited          CloseCode = 4008
	CloseSessionTimedOut      CloseCode = 4009
	CloseInvalidShard         CloseCode = 4010
	CloseShardingRequired     CloseCode = 4011
	CloseInvalidAPIVersion    CloseCode = 4012
	CloseInvalidIntents       CloseCode = 4013
	CloseDisallowedIntents    CloseCode = 4014
)

// Fatal is a close no reconnect can get past: the token, the shard, the API version or the
// intents are wrong, and only a change on this side fixes them.
func (c CloseCode) Fatal() bool {
	switch c {
	case CloseAuthenticationFailed, CloseInvalidShard, CloseShardingRequired,
		CloseInvalidAPIVersion, CloseInvalidIntents, CloseDisallowedIntents:
		return true
	}
	return false
}

// Resumable is a close after which the session can be resumed rather than identified anew.
func (c CloseCode) Resumable() bool {
	switch c {
	case CloseInvalidSeq, CloseSessionTimedOut:
		return false
	}
	return !c.Fatal()
}

type CloseError struct {
	Code   CloseCode
	Reason string
}

func (e *CloseError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("Discord closed the gateway with %d", e.Code)
	}
	return fmt.Sprintf("Discord closed the gateway with %d: %s", e.Code, e.Reason)
}
