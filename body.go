package extproc

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log"
)

type BodyType struct {
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Type
	ContentType string // the body content type, if applicable, but almost always present

	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Encoding
	ContentEncoding string // the body content encoding (compression), if applicable

	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Transfer-Encoding
	TransferEncoding string // The HTTP/1.1 transfer encoding used, not valid in HTTP/2
}

// New "type" of a request/response body inferring from related headers
func NewBodyTypeFromHeaders(headers *AllHeaders) BodyType {
	cts, _ := headers.GetHeaderValueAsString("content-type")
	ces, _ := headers.GetHeaderValueAsString("content-encoding")
	tes, _ := headers.GetHeaderValueAsString("transfer-encoding")
	return BodyType{
		ContentType:      cts,
		ContentEncoding:  ces,
		TransferEncoding: tes,
	}
}

// Reply true if the body bytes should be interpretted as compressed
// data with strategies defined by the type's stored headers.
func (b *BodyType) IsCompressed() bool {
	if len(b.ContentEncoding) > 0 {
		return true
	}
	if len(b.TransferEncoding) > 0 && !b.IsChunked() {
		return true
	}
	return false
}

// Return the declared encoding
func (b *BodyType) Encoding() string {
	if len(b.ContentEncoding) > 0 {
		return b.ContentEncoding
	}
	if len(b.TransferEncoding) > 0 && !b.IsChunked() {
		return b.TransferEncoding
	}
	return "none"
}

// Reply true if the body bytes should be interpretted as chunked.
// This is valid for HTTP/1.1 only, and transfer-encoding: chunked
// can cause other issues.
func (b *BodyType) IsChunked() bool {
	return b.TransferEncoding == "chunked"
}

// Type for "wrapping" bodies that may involve streaming and/or encoding,
// particularly concerning body compression with standard strategies.
type EncodedBody struct {
	Type         BodyType // the "type" of this body (according to headers)
	Value        []byte   // body bytes, potentially accumulated in streaming/chunking
	MaxSize      int64    // maximum allowable size of a buffer; -1 for no limit
	Complete     bool     // flag to identify if the body is complete
	Decompressed bool     // flag to identify if decompression was successful
}

// Initializer for an `EncodedBody` when headers are known, and thus
// the intended type, encoding, and (in HTTP/1.1) transfer style is
// known.
func NewEncodedBodyFromHeaders(headers *AllHeaders) *EncodedBody {
	eb := EncodedBody{
		Type:    NewBodyTypeFromHeaders(headers),
		Value:   make([]byte, 0),
		MaxSize: -1,
	}
	if !eb.IsCompressed() {
		eb.Decompressed = true // zero value is misleading if not compressed
	}
	return &eb
}

// Return the current "content length" of an encoded body, in bytes.
// This is not necessarily a request/response's full content length
// when streaming over multiple messages.
//
// Note results are valid up to about 4GB with uint32.
func (b *EncodedBody) CurrentContentLength() uint32 {
	return uint32(len(b.Value))
}

// Reply true if the body bytes should be interpretted as compressed
// data with strategies defined by the type's stored headers.
func (b *EncodedBody) IsCompressed() bool {
	return b.Type.IsCompressed()
}

// Return the declared encoding
func (b *EncodedBody) Encoding() string {
	return b.Type.Encoding()
}

// Reply true if the body bytes should be interpretted as chunked.
// This is valid for HTTP/1.1 only, and transfer-encoding: chunked
// can cause other issues.
func (b *EncodedBody) IsChunked() bool {
	return b.Type.IsChunked()
}

// Append a "chunk" of bytes to stored bytes. Intended to be used
// when the SDK is accumulating streaming body bytes on behalf of
// an implementation to simplify access to a full body. Envoy can
// do this as well, of course, with BUFFERED or BUFFERED_PARTIAL
// body modes. It might, though, be of interest to reduce memory
// pressure on the actual proxy, allowing the external processor
// to customize behavior around handling of streaming bodies
func (b *EncodedBody) AppendChunk(chunk []byte) error {
	if chunk != nil {
		if b.MaxSize > 0 {
			newSize := len(chunk) + len(b.Value)
			if newSize > int(b.MaxSize) {
				return errors.New("appending chunk would violate size limit")
			}
		}
		b.Value = append(b.Value, chunk...)
	}
	return nil
}

// Decompress the received body according to the stored header values
// describing the compression strategy. No-op when not compressed, returns
// an error if incomplete, or returns an error when decompression fails.
//
// Sets the struct flag `Decompressed` to identify success/failure for
// SDK users to identify if the stored bytes can be interpreted as "real"
// content.
func (b *EncodedBody) DecompressBody() error {
	if !b.IsCompressed() {
		b.Decompressed = true
		return nil
	}
	if !b.Complete {
		b.Decompressed = false
		return errors.New("cannot decompress an incomplete body")
	}

	// TODO: declare and check supported encoding/compression strategies
	encoding := b.Encoding()
	if encoding == "gzip" {
		unzipped, err := gUnzipData(b.Value)
		if err != nil {
			b.Decompressed = false
			log.Printf("gzip decompression failed: %v\n", err)
			return errors.New("gzip decompression failed")
		}
		b.Value = unzipped
		return nil
	}

	log.Printf("Decompression for \"%s\" not yet implemented\n", encoding)
	b.Decompressed = false
	return errors.New("Unsupported encoding/compression strategy")
}

// https://gist.github.com/alex-ant/aeaaf497055590dacba760af24839b8d
func gUnzipData(data []byte) (unzipped []byte, err error) {
	b := bytes.NewBuffer(data)
	var r io.Reader
	r, err = gzip.NewReader(b)
	if err != nil {
		return
	}
	var result bytes.Buffer
	_, err = result.ReadFrom(r)
	if err != nil {
		return
	}
	unzipped = result.Bytes()
	return
}
