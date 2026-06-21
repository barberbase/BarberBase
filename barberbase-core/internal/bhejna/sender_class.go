package bhejna

type SenderClass string

const (
	SenderPlatform SenderClass = "platform"
	SenderCustomer SenderClass = "customer"
)

var platformTemplates = map[string]struct{}{
	"bb_staff_otp":      {},
	"bb_weekly_summary": {},
}

func ClassFor(templateCode string) SenderClass {
	if _, ok := platformTemplates[templateCode]; ok {
		return SenderPlatform
	}
	return SenderCustomer
}
