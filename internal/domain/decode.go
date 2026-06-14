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

	// DetectedChain — слой-1 авто-детект обёрток/кодировки, напр.
	// ["base64","gzip","protobuf"]. Последний элемент — вид ядра payload'а.
	DetectedChain []string
}

// TypeGuess — кандидат при авто-детекте типа сообщения по байтам payload'а.
// Fields — сколько полей реально заполнилось (рекурсивно), Unknown — байт
// нераспознанных полей (0 = схема объясняет все данные), Depth — глубина
// вложенности. Чем больше Fields и меньше Unknown — тем увереннее кандидат.
type TypeGuess struct {
	FullType string
	// ProtoFile — путь файла с этим типом (относительно proto root, slash).
	// Нужен, чтобы при выборе кандидата подставить и Proto file для декода.
	ProtoFile string
	Fields    int
	Unknown   int
	Depth     int
}

type Decoder interface {
	ValidateMessageType(ctx context.Context, protoRoot, fullType, protoAbs string) error
	Decode(ctx context.Context, req DecodeRequest) (DecodeResult, error)
	// GuessType возвращает ранжированных кандидатов типа сообщения для payload'а
	// (обёртки снимаются автоматически), лучший — первым.
	GuessType(ctx context.Context, protoRoot, protoAbs string, payload []byte) ([]TypeGuess, error)
}
