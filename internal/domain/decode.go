package domain

import "context"

type OutputFormat string

const (
	OutputFormatJSON OutputFormat = "JSON"
	OutputFormatRAW  OutputFormat = "RAW"
)

type DecodeRequest struct {
	ProtoRoot string
	ProtoFile string
	FullType  string

	Gzip bool

	Format OutputFormat
	Bytes  []byte
}

type DecodeResult struct {
	Raw    string
	Pretty string

	// AutoDetectedGzip is true when decoder had to gunzip input bytes automatically
	// after a failed normal decode attempt.
	AutoDetectedGzip bool
}

type Decoder interface {
	ValidateMessageType(ctx context.Context, protoRoot, fullType, protoAbs string) error
	Decode(ctx context.Context, req DecodeRequest) (DecodeResult, error)
}
