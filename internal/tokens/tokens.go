// Package tokens holds the single shared BPE encoder ccmcp uses to estimate
// token counts.
//
// Anthropic doesn't publish its tokenizer, so OpenAI's cl100k_base is the
// standard close-enough analog (within ~5% on prose, much closer than the
// 4-chars-per-token rule of thumb). Every count derived from it is an estimate
// and must be presented as one.
package tokens

import (
	"fmt"
	"sync"
	"time"

	"github.com/pkoukk/tiktoken-go"
)

// fetchTimeout bounds how long Encoder waits for the BPE table to warm.
// Encoder sits on the TUI render path (the per-turn context-cost estimate),
// so tiktoken-go's unbounded first-call HTTP fetch of the BPE table - into a
// purgeable $TMPDIR cache, with no timeout - must not be able to freeze the
// UI on a cold cache and a slow or hostile network.
//
// This is a var, not a const, so tests can shrink it to make the timeout
// path hermetic and fast instead of waiting out a real 10s deadline.
var fetchTimeout = 10 * time.Second

var (
	encOnce sync.Once
	enc     *tiktoken.Tiktoken
	encErr  error

	// warmDone closes once the background warm (started by the first
	// Encoder call) has stored its result in enc/encErr. It is created
	// inside encOnce.Do, so it exists only after the warm has been kicked
	// off exactly once.
	warmDone chan struct{}

	// warmFn performs the actual (possibly slow, network-fetching) load.
	// It is a package var, not a direct call to tiktoken.GetEncoding, so
	// tests can substitute a stub that never touches the network or the
	// real tiktoken-go package cache.
	warmFn = func() (*tiktoken.Tiktoken, error) {
		return tiktoken.GetEncoding("cl100k_base")
	}
)

// Encoder returns the process-wide memoised cl100k_base encoder.
//
// The first call starts the warm exactly once, in the background, and does
// not wait for it to finish before returning control to the select below.
// Every caller - the one that started the warm and any that arrive
// concurrently or later - waits at most fetchTimeout for it to complete. A
// caller that times out gets an error rather than blocking forever; the warm
// itself keeps running, so a later call (after the network eventually
// responds, or never) still observes the real outcome via warmDone, rather
// than being left permanently stuck on a timeout that has nothing to do with
// its own wait.
func Encoder() (*tiktoken.Tiktoken, error) {
	encOnce.Do(func() {
		warmDone = make(chan struct{})
		go func() {
			defer close(warmDone)
			enc, encErr = warmFn()
		}()
	})

	select {
	case <-warmDone:
		return enc, encErr
	case <-time.After(fetchTimeout):
		return nil, fmt.Errorf("tokens: encoder warm timed out after %s", fetchTimeout)
	}
}

// Count returns the BPE token count of s. An empty string is 0 tokens and
// never an error.
func Count(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	e, err := Encoder()
	if err != nil {
		return 0, err
	}
	return len(e.Encode(s, nil, nil)), nil
}

// CountLines sums Count over ss. Counting each line separately (rather than
// concatenating) keeps per-item attribution exact - a joined string can merge
// tokens across a boundary and make the parts not sum to the whole.
func CountLines(ss []string) (int, error) {
	total := 0
	for _, s := range ss {
		n, err := Count(s)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
