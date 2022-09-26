package alsftp

const (
	int16Bytes = 2
	int64Bytes = 8

	DefaultClientLogType = 1
	DefaultServerLogType = 0

	MaxPartitionSize     = 65507 - int64Bytes - int16Bytes - 1 //bytes
	DefaultPartitionSize = 1472 - int64Bytes - int16Bytes - 1  //bytes

	DefaultRetries = 5
	MaxRetries     = 10

	DefaultAcknowledgementTimeout = 500   //ms
	MaxAcknowledgementTimeout     = 60000 //ms

	DefaultRetryDelay = 250  //ms
	MaxRetryDelay     = 1000 //ms

	DefaultTcpTimeout = 5000 //ms

	opOpenConnection = 0x1F
	opSaveFilename   = 0x2F
	opSaveFilePart   = 0x3F
	opEOF            = 0x4F
	opDownload       = 0x5F

	ackOK = 0xFF

	ackOpenConnection = 0x1A
	ackSaveFilename   = 0x2A
	ackFilePart       = 0x3A
	ackEOF            = 0x4A
	ackDownload       = 0x5A

	maxUdpSize = 1 << 16
)

type (
	request struct {
		Type    byte
		Content []byte
	}

	dataPartition struct {
		Id      uint64
		Content []byte
	}
)
