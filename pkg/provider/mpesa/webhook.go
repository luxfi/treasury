package mpesa

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CallbackEvent is the normalized shape produced by ParseCallback.
// treasuryd consumes this from its Daraja callback HTTP handlers.
type CallbackEvent struct {
	Provider   string          `json:"provider"`
	EventType  string          `json:"event_type"` // stk_callback, b2c_result, balance_result, status_result, reversal_result, timeout
	ExternalID string          `json:"external_id"`
	Status     string          `json:"status"` // completed, failed, pending
	Amount     string          `json:"amount,omitempty"`
	Currency   string          `json:"currency,omitempty"`
	MSISDN     string          `json:"msisdn,omitempty"`
	ResultCode int             `json:"result_code"`
	ResultDesc string          `json:"result_desc,omitempty"`
	Raw        json.RawMessage `json:"raw"`
}

// ParseCallback parses a Daraja callback body posted to ResultURL,
// QueueTimeoutURL, or the STK CallBackURL. The callback shape varies by
// operation; we discriminate by which top-level key is present.
func ParseCallback(body []byte) (*CallbackEvent, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("mpesa: empty callback body")
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("mpesa callback: %w", err)
	}

	if raw, ok := probe["Body"]; ok {
		var b struct {
			StkCallback *stkCallback `json:"stkCallback"`
		}
		if err := json.Unmarshal(raw, &b); err == nil && b.StkCallback != nil {
			return parseSTK(b.StkCallback, body), nil
		}
	}
	if raw, ok := probe["Result"]; ok {
		var r resultEnvelope
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("mpesa result: %w", err)
		}
		return parseResult(&r, body), nil
	}
	return nil, fmt.Errorf("mpesa: unrecognized callback shape")
}

type stkCallback struct {
	MerchantRequestID string `json:"MerchantRequestID"`
	CheckoutRequestID string `json:"CheckoutRequestID"`
	ResultCode        int    `json:"ResultCode"`
	ResultDesc        string `json:"ResultDesc"`
	CallbackMetadata  *struct {
		Item []struct {
			Name  string          `json:"Name"`
			Value json.RawMessage `json:"Value"`
		} `json:"Item"`
	} `json:"CallbackMetadata,omitempty"`
}

func parseSTK(c *stkCallback, raw []byte) *CallbackEvent {
	ev := &CallbackEvent{
		Provider:   "mpesa",
		EventType:  "stk_callback",
		ExternalID: c.CheckoutRequestID,
		ResultCode: c.ResultCode,
		ResultDesc: c.ResultDesc,
		Currency:   "KES",
		Raw:        raw,
	}
	if c.ResultCode == 0 {
		ev.Status = "completed"
	} else {
		ev.Status = "failed"
	}
	if c.CallbackMetadata != nil {
		for _, it := range c.CallbackMetadata.Item {
			switch it.Name {
			case "Amount":
				var f float64
				if json.Unmarshal(it.Value, &f) == nil {
					ev.Amount = strconv.FormatFloat(f, 'f', -1, 64)
				}
			case "PhoneNumber":
				var n int64
				if json.Unmarshal(it.Value, &n) == nil {
					ev.MSISDN = strconv.FormatInt(n, 10)
				} else {
					var s string
					if json.Unmarshal(it.Value, &s) == nil {
						ev.MSISDN = s
					}
				}
			}
		}
	}
	return ev
}

type resultEnvelope struct {
	ResultType               int    `json:"ResultType"`
	ResultCode               int    `json:"ResultCode"`
	ResultDesc               string `json:"ResultDesc"`
	OriginatorConversationID string `json:"OriginatorConversationID"`
	ConversationID           string `json:"ConversationID"`
	TransactionID            string `json:"TransactionID"`
	ResultParameters         *struct {
		ResultParameter []struct {
			Key   string          `json:"Key"`
			Value json.RawMessage `json:"Value"`
		} `json:"ResultParameter"`
	} `json:"ResultParameters,omitempty"`
	ReferenceData *struct {
		ReferenceItem json.RawMessage `json:"ReferenceItem"`
	} `json:"ReferenceData,omitempty"`
}

func parseResult(r *resultEnvelope, raw []byte) *CallbackEvent {
	ev := &CallbackEvent{
		Provider:   "mpesa",
		ExternalID: r.OriginatorConversationID,
		ResultCode: r.ResultCode,
		ResultDesc: r.ResultDesc,
		Currency:   "KES",
		Raw:        raw,
	}
	if r.ResultCode == 0 {
		ev.Status = "completed"
	} else if r.ResultCode == 1037 || strings.Contains(strings.ToLower(r.ResultDesc), "timeout") {
		ev.Status = "failed"
		ev.EventType = "timeout"
	} else {
		ev.Status = "failed"
	}
	if ev.EventType == "" {
		ev.EventType = classifyResult(r)
	}
	if r.ResultParameters != nil {
		for _, p := range r.ResultParameters.ResultParameter {
			switch p.Key {
			case "TransactionAmount", "Amount":
				var f float64
				if json.Unmarshal(p.Value, &f) == nil {
					ev.Amount = strconv.FormatFloat(f, 'f', -1, 64)
				}
			case "ReceiverPartyPublicName", "DebitPartyName":
				var s string
				if json.Unmarshal(p.Value, &s) == nil && ev.MSISDN == "" {
					// e.g. "254712345678 - John Doe"
					if i := strings.IndexByte(s, ' '); i > 0 {
						ev.MSISDN = strings.TrimSpace(s[:i])
					}
				}
			case "MSISDN":
				var s string
				if json.Unmarshal(p.Value, &s) == nil {
					ev.MSISDN = s
				}
			}
		}
	}
	return ev
}

func classifyResult(r *resultEnvelope) string {
	d := strings.ToLower(r.ResultDesc)
	switch {
	case strings.Contains(d, "b2c") || strings.Contains(d, "business payment"):
		return "b2c_result"
	case strings.Contains(d, "balance"):
		return "balance_result"
	case strings.Contains(d, "reversal"):
		return "reversal_result"
	case strings.Contains(d, "status"):
		return "status_result"
	}
	return "result"
}
