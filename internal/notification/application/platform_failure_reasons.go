package application

import (
	"strconv"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// platformFailureCode turns the error Meta put on a failed receipt into the
// reason staff see, so a closed window reads differently from a number that is
// not on WhatsApp.
func platformFailureCode(status whatsAppWebhookStatus) string {
	for _, item := range status.Errors {
		switch metaErrorCode(item["code"]) {
		case 131047:
			return ErrorCodeWindowLocked
		case 131026:
			return ErrorCodeInvalidNumber
		case 130472, 131049, 131050:
			return ErrorCodeUndeliverable
		}
	}
	return ErrorCodeSendFailed
}

func metaErrorCode(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		code, _ := strconv.Atoi(typed)
		return code
	}
	return 0
}

// sendFailurePayload is the error stored on an outbound message whose send was
// refused, in the same words a failed receipt would later use.
func sendFailurePayload(cause error) map[string]any {
	apiErr, ok := domain.AsWhatsAppAPIError(cause)
	if !ok {
		return errorPayload(ErrorCodeSendFailed, "")
	}
	switch apiErr.Code {
	case 131047:
		return errorPayload(ErrorCodeWindowLocked, "")
	case 131026:
		return errorPayload(ErrorCodeInvalidNumber, "")
	case 130472, 131049, 131050:
		return errorPayload(ErrorCodeUndeliverable, "")
	}
	return errorPayload(ErrorCodeSendFailed, apiErr.PlainMessage())
}
