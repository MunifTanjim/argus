package api

import (
	"errors"
	"time"
)

// StartedAt is when this process started. An exec in place starts it again, so
// a client can tell that a restart completed.
var StartedAt = time.Now().UTC()

func StartedAtRFC3339() string { return StartedAt.Format(time.RFC3339Nano) }

func IsMethodNotFound(err error) bool {
	var re *RPCError
	return errors.As(err, &re) && re.Code == CodeMethodNotFound
}
