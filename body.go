package extproc

type EncodedBody struct {
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Type
	ContentType string

	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Encoding
	ContentEncoding string

	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Transfer-Encoding
	// Not valid in HTTP/2
	TransferEncoding string

	Value []byte
}

func NewEncodedBodyFromHeaders(headers *AllHeaders) *EncodedBody {
	cts, _ := headers.GetHeaderValueAsString("content-type")
	ces, _ := headers.GetHeaderValueAsString("content-encoding")
	tes, _ := headers.GetHeaderValueAsString("transfer-encoding")
	return &EncodedBody{
		ContentType:      cts,
		ContentEncoding:  ces,
		TransferEncoding: tes,
		Value:            make([]byte, 0),
	}
}

func (b *EncodedBody) CurrentContentLength() uint32 {
	return uint32(len(b.Value))
}

func (b *EncodedBody) IsCompressed() bool {
	if len(b.ContentEncoding) > 0 {
		return true
	}
	if len(b.TransferEncoding) > 0 && !b.IsChunked() {
		return true
	}
	return false
}

func (b *EncodedBody) IsChunked() bool {
	return b.TransferEncoding == "chunked"
}

func (b *EncodedBody) AddChunk(chunk []byte) {
	if chunk == nil {
		return
	}
	b.Value = append(b.Value, chunk...)
}
