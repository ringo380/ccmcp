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
	"sync/atomic"
	"time"

	"github.com/pkoukk/tiktoken-go"
)

// fetchTimeout bounds how long a caller's *own wait* for the BPE table can
// take - not the underlying fetch. tiktoken.GetEncoding takes no
// context.Context and tiktoken-go exposes no cancellation hook, so the
// background warm goroutine's HTTP request cannot itself be cancelled: if
// the network hangs outright rather than erroring, that one goroutine (and
// its connection) lives for the remaining life of the process. That leak is
// bounded to exactly one goroutine, ever, by encOnce - Encoder never starts a
// second warm no matter how many times callers time out - so it is an
// accepted, capped cost rather than an unbounded one. fetchTimeout only
// decides how long any single Encoder call is willing to sit blocked waiting
// on that goroutine's result.
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

	// warmTimedOut records that some earlier Encoder call already waited
	// out a full fetchTimeout without the warm completing. Encoder is on
	// the TUI render path, so once this is true, later calls must not
	// block again - a persistently slow or hanging network would otherwise
	// re-stall the UI for fetchTimeout on every single render. Once set,
	// callers instead do a non-blocking check of warmDone: the real result
	// if the warm has since finished, or errStillLoading if it hasn't.
	warmTimedOut atomic.Bool

	// warmFn performs the actual (possibly slow, network-fetching) load.
	// It is a package var, not a direct call to tiktoken.GetEncoding, so
	// tests can substitute a stub that never touches the network or the
	// real tiktoken-go package cache.
	warmFn = func() (*tiktoken.Tiktoken, error) {
		return tiktoken.GetEncoding("cl100k_base")
	}
)

// errStillLoading is returned by a non-blocking Encoder call made after some
// earlier call already timed out waiting for the warm. It is short and
// worded to read sensibly when the TUI truncates costUnavailable() text to
// 60 runes and shows it as the reason next to a "-".
var errStillLoading = fmt.Errorf("tokens: encoder still loading")

// Encoder returns the process-wide memoised cl100k_base encoder.
//
// The first call starts the warm exactly once, in the background, and does
// not wait for it to finish before returning control to the select below.
// The first wave of callers - the one that started the warm and any that
// arrive concurrently before any wait has timed out - each wait up to
// fetchTimeout for it to complete. Once any caller has timed out, every
// later call skips the blocking wait entirely and does a non-blocking check
// of warmDone instead: it returns the real encoder if the warm has since
// finished, or errStillLoading immediately if it hasn't. This caps the
// render-path cost at one fetchTimeout stall for the whole process - a
// persistently slow network freezes the UI once, not on every frame - while
// the warm goroutine itself keeps running in the background and any render
// after it finishes picks up the real result via warmDone.
func Encoder() (*tiktoken.Tiktoken, error) {
	encOnce.Do(func() {
		warmDone = make(chan struct{})
		go func() {
			defer close(warmDone)
			enc, encErr = warmFn()
		}()
	})

	if warmTimedOut.Load() {
		select {
		case <-warmDone:
			return enc, encErr
		default:
			return nil, errStillLoading
		}
	}

	select {
	case <-warmDone:
		return enc, encErr
	case <-time.After(fetchTimeout):
		warmTimedOut.Store(true)
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
