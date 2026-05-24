package desktop

type EncoderOptions struct {
	Codec   string
	Quality int
}

// Encoder is reserved for JPEG/H.264/AV1 encoders. The MVP sends simulated
// text frames while keeping this boundary available for real video frames.
type Encoder interface {
	Encode(Frame) (Frame, error)
	Close() error
}
