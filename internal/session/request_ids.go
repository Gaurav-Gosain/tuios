package session

import "time"

// roundTripTimeout is how long a round trip waits for its answer. An answer
// that comes later is dropped when it arrives (see routeReply). A variable so
// a test can make a request give up without waiting the full time.
var roundTripTimeout = 30 * time.Second
