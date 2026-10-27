package local

import (
	"bufio"
	"crypto/hmac"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// emptyStringSHA256 is the SHA-256 hash of the empty string. It appears in
// the chunk and trailer string-to-sign as the canonical-headers slot.
var emptyStringSHA256 = sha256Hex(nil)

// chunkSigningContext carries the data needed to verify per-chunk SigV4
// signatures for STREAMING-AWS4-HMAC-SHA256-PAYLOAD[-TRAILER] payloads.
type chunkSigningContext struct {
	// algorithm is the chunk-signing algorithm:
	// "AWS4-HMAC-SHA256-PAYLOAD" or "AWS4-HMAC-SHA256-PAYLOAD-TRAILER".
	algorithm string

	// signingKey is the SigV4 derived signing key (kSigning).
	signingKey []byte

	// amzDate is the X-Amz-Date header value (YYYYMMDDTHHMMSSZ).
	amzDate string

	// scope is "<date>/<region>/<service>/aws4_request".
	scope string

	// prevSignature is the rolling previous-signature, seeded with the
	// request's seed signature from the Authorization header. It is
	// updated to the chunk's signature after each chunk is verified.
	prevSignature string
}

// chunkedReader decodes an aws-chunked encoded body. Each Read returns
// decoded payload bytes only; chunk framing, optional trailers and the
// terminating CRLF are consumed transparently.
//
// When sign is non-nil, every data chunk's "chunk-signature=" parameter is
// verified before its bytes are returned. A mismatch surfaces as a Read
// error.
type chunkedReader struct {
	br   *bufio.Reader
	sign *chunkSigningContext

	// chunk holds the current decoded chunk's bytes; pos is how many of
	// those bytes have been served to callers.
	chunk []byte
	pos   int

	// finished is set once the terminating zero-length chunk (and trailer,
	// if any) has been fully consumed.
	finished bool
}

// newChunkedReader wraps r in an aws-chunked decoder. If sign is non-nil,
// per-chunk signatures are verified.
func newChunkedReader(r io.Reader, sign *chunkSigningContext) *chunkedReader {
	return &chunkedReader{
		br:   bufio.NewReader(r),
		sign: sign,
	}
}

// Read returns decoded payload bytes from the wrapped aws-chunked stream.
func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.chunk) {
		if c.finished {
			return 0, io.EOF
		}

		err := c.nextChunk()
		if err != nil {
			return 0, err
		}

		if c.finished && c.pos >= len(c.chunk) {
			return 0, io.EOF
		}
	}

	n := copy(p, c.chunk[c.pos:])
	c.pos += n
	return n, nil
}

// nextChunk reads the next chunk header, optionally verifies its signature,
// loads its bytes into c.chunk, and consumes the terminating CRLF. If the
// chunk is the terminating zero-length chunk, any trailer is consumed and
// the stream is marked finished.
func (c *chunkedReader) nextChunk() error {
	c.chunk = c.chunk[:0]
	c.pos = 0

	header, err := readLine(c.br)
	if err != nil {
		return fmt.Errorf("read chunk header: %w", err)
	}

	sizeField, sigField := splitChunkHeader(header)

	size, err := strconv.ParseInt(sizeField, 16, 64)
	if err != nil || size < 0 {
		return fmt.Errorf("invalid chunk size %q", sizeField)
	}

	if size > 0 {
		if cap(c.chunk) < int(size) {
			c.chunk = make([]byte, size)
		} else {
			c.chunk = c.chunk[:size]
		}

		_, err = io.ReadFull(c.br, c.chunk)
		if err != nil {
			return fmt.Errorf("read chunk body: %w", err)
		}

		err = consumeCRLF(c.br)
		if err != nil {
			return err
		}

		err = c.verifyChunkSignature(sigField, c.chunk)
		if err != nil {
			return err
		}

		return nil
	}

	// size == 0: terminating chunk. The chunk body is empty but for
	// signed payloads still has its own signature over the empty body.
	err = consumeCRLF(c.br)
	if err != nil {
		// AWS streaming with trailer omits the CRLF after the
		// zero-chunk header (the trailer block follows directly).
		// Tolerate either layout by allowing consumeCRLF to be
		// optional here: if the next bytes are not CRLF, treat them
		// as the trailer block.
		if !errors.Is(err, errExpectedCRLF) {
			return err
		}
	}

	err = c.verifyChunkSignature(sigField, nil)
	if err != nil {
		return err
	}

	err = c.consumeTrailer()
	if err != nil {
		return err
	}

	c.finished = true
	return nil
}

// verifyChunkSignature checks the chunk-signature line for a chunk whose
// decoded body is data. For unsigned streams the signature is ignored.
func (c *chunkedReader) verifyChunkSignature(sig string, data []byte) error {
	if c.sign == nil {
		return nil
	}

	if sig == "" {
		return errors.New("missing chunk signature")
	}

	expected := computeChunkSignature(c.sign, sha256Hex(data))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return errors.New("chunk signature mismatch")
	}

	c.sign.prevSignature = sig
	return nil
}

// consumeTrailer reads any trailing headers that appear after the final
// zero-length chunk and consumes the closing empty line. The trailing
// signature on signed-with-trailer payloads is parsed but not verified.
func (c *chunkedReader) consumeTrailer() error {
	for {
		line, err := readLine(c.br)
		if errors.Is(err, io.EOF) {
			// Streams without an explicit closing CRLF are
			// tolerated: the body ended at EOF.
			return nil
		}

		if err != nil {
			return fmt.Errorf("read trailer line: %w", err)
		}

		if line == "" {
			return nil
		}
	}
}

// splitChunkHeader splits a chunk header line into its size field and the
// optional chunk-signature value. The header has the form:
//
//	<hex-size>[;chunk-signature=<hex>][;...]
func splitChunkHeader(header string) (size, signature string) {
	parts := strings.Split(header, ";")
	size = strings.TrimSpace(parts[0])
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		v, ok := strings.CutPrefix(p, "chunk-signature=")
		if ok {
			signature = v
		}
	}

	return size, signature
}

// readLine reads a CRLF-terminated line from r and returns it without the
// terminating CRLF.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimRight(line, "\r\n"), nil
}

// errExpectedCRLF is returned by consumeCRLF when the next two bytes are
// not "\r\n". It is sentinel-only so that callers can distinguish the
// optional-CRLF case at the end of a stream.
var errExpectedCRLF = errors.New("expected CRLF")

// consumeCRLF reads exactly "\r\n" from r.
func consumeCRLF(r *bufio.Reader) error {
	buf := make([]byte, 2)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return fmt.Errorf("read chunk terminator: %w", err)
	}

	if buf[0] != '\r' || buf[1] != '\n' {
		return errExpectedCRLF
	}

	return nil
}

// computeChunkSignature returns the expected hex chunk signature for a chunk
// whose decoded SHA-256 hash is chunkHash, given the rolling signing context.
//
// The string-to-sign is:
//
//	<algorithm> + "\n" +
//	<amzDate> + "\n" +
//	<scope> + "\n" +
//	<previous-signature> + "\n" +
//	sha256("") + "\n" +
//	<chunkHash>
func computeChunkSignature(ctx *chunkSigningContext, chunkHash string) string {
	stringToSign := strings.Join([]string{
		ctx.algorithm,
		ctx.amzDate,
		ctx.scope,
		ctx.prevSignature,
		emptyStringSHA256,
		chunkHash,
	}, "\n")

	return hmacSHA256Hex(ctx.signingKey, stringToSign)
}
