package gows

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"go.mau.fi/util/random"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// ButtonKind is the type of native flow button in an interactive message.
// The zero value means "no type" and is rejected by ValidateInteractive.
type ButtonKind int

const (
	ButtonReply ButtonKind = iota + 1
	ButtonURL
	ButtonCall
	ButtonCopy
	ButtonPix
	ButtonRaw
)

// Native flow button names and biz node attributes (Evolution API PR #2526).
const (
	nativeFlowQuickReply  = "quick_reply"
	nativeFlowCtaURL      = "cta_url"
	nativeFlowCtaCall     = "cta_call"
	nativeFlowCtaCopy     = "cta_copy"
	nativeFlowPaymentInfo = "payment_info"

	bizInteractiveType    = "native_flow"
	bizInteractiveVersion = "1"
	bizNativeFlowVersion  = "9"
	bizNativeFlowName     = "mixed"
	// payment_info is announced by name on <biz> itself; with the "mixed" node the phone drops it
	bizNativeFlowNameAttr = "native_flow_name"

	interactiveMaxButtons      = 10
	interactiveMaxReplyButtons = 3
	interactiveMaxCtaButtons   = 2
)

// PixPayment is a static PIX key. KeyType is one of PHONE, EMAIL, CPF, CNPJ, EVP.
type PixPayment struct {
	MerchantName string
	Key          string
	KeyType      string
	Currency     string
}

type InteractiveButton struct {
	Kind          ButtonKind
	DisplayText   string
	ID            string
	URL           string
	MerchantURL   string
	PhoneNumber   string
	CopyCode      string
	Pix           *PixPayment
	RawName       string
	RawParamsJSON string
}

// InteractiveHeaderMedia holds the uploaded header media, exactly one field set.
type InteractiveHeaderMedia struct {
	Image    *waE2E.ImageMessage
	Video    *waE2E.VideoMessage
	Document *waE2E.DocumentMessage
}

type Interactive struct {
	HeaderTitle    string
	HeaderSubtitle string
	// HasHeaderMedia is true when the request has header media (set before upload)
	HasHeaderMedia bool
	// HeaderMedia is filled by the caller after upload, before BuildInteractiveMessage
	HeaderMedia *InteractiveHeaderMedia
	Body        string
	Footer      string
	Buttons     []InteractiveButton
}

func interactiveError(format string, args ...any) error {
	return fmt.Errorf("interactive: "+format, args...)
}

// ValidateInteractive checks structure and button mixing rules.
// Warnings are non-fatal and should only be logged (currently none are produced).
// Raw buttons count for the total limits and pix exclusivity, but are exempt from the other mixing rules.
func ValidateInteractive(in *Interactive) (warnings []string, err error) {
	if in == nil || strings.TrimSpace(in.Body) == "" {
		return nil, interactiveError("body is required")
	}
	if len(in.Buttons) == 0 {
		return nil, interactiveError("at least one button is required")
	}
	if len(in.Buttons) > interactiveMaxButtons {
		return nil, interactiveError("too many buttons (max %d)", interactiveMaxButtons)
	}

	counts := map[ButtonKind]int{}
	ids := map[string]bool{}
	for i, b := range in.Buttons {
		switch b.Kind {
		case ButtonReply, ButtonURL, ButtonCall, ButtonCopy:
			if strings.TrimSpace(b.DisplayText) == "" {
				return nil, interactiveError("button[%d] displayText is required", i)
			}
		case ButtonPix, ButtonRaw:
		default:
			return nil, interactiveError("button[%d] has no type", i)
		}
		counts[b.Kind]++

		switch b.Kind {
		case ButtonReply:
			if b.ID != "" {
				if ids[b.ID] {
					return nil, interactiveError("duplicate button id %q", b.ID)
				}
				ids[b.ID] = true
			}
		case ButtonURL:
			if !isHTTPURL(b.URL) {
				return nil, interactiveError("button[%d] url must be http(s)", i)
			}
			if b.MerchantURL != "" && !isHTTPURL(b.MerchantURL) {
				return nil, interactiveError("button[%d] merchantUrl must be http(s)", i)
			}
		case ButtonCall:
			if strings.TrimSpace(b.PhoneNumber) == "" {
				return nil, interactiveError("button[%d] phoneNumber is required", i)
			}
		case ButtonCopy:
			if b.CopyCode == "" {
				return nil, interactiveError("button[%d] copyCode is required", i)
			}
		case ButtonPix:
			pix := b.Pix
			if pix == nil {
				pix = &PixPayment{}
			}
			switch {
			case strings.TrimSpace(pix.MerchantName) == "":
				return nil, interactiveError("pix merchantName is required")
			case strings.TrimSpace(pix.Key) == "":
				return nil, interactiveError("pix key is required")
			case strings.TrimSpace(pix.KeyType) == "":
				return nil, interactiveError("pix keyType is required")
			}
		case ButtonRaw:
			if strings.TrimSpace(b.RawName) == "" || !json.Valid([]byte(b.RawParamsJSON)) {
				return nil, interactiveError("button[%d] raw requires name and valid paramsJson", i)
			}
			// A raw payment_info gets the PIX <biz> node, so it follows the PIX rules
			if b.RawName == nativeFlowPaymentInfo {
				counts[ButtonPix]++
			}
		}
	}

	if counts[ButtonPix] > 0 && len(in.Buttons) > 1 {
		return nil, interactiveError("pix button must be the only button")
	}
	ctas := counts[ButtonURL] + counts[ButtonCall] + counts[ButtonCopy]
	if counts[ButtonReply] > 0 && ctas+counts[ButtonPix] > 0 {
		return nil, interactiveError("quick_reply cannot be mixed with other button types")
	}
	if counts[ButtonReply] > interactiveMaxReplyButtons {
		return nil, interactiveError("quick_reply supports up to %d buttons", interactiveMaxReplyButtons)
	}
	if ctas > interactiveMaxCtaButtons {
		return nil, interactiveError("cta buttons (url/call/copy) support up to %d buttons", interactiveMaxCtaButtons)
	}
	return warnings, nil
}

type nativeFlowAmount struct {
	Value  int `json:"value"`
	Offset int `json:"offset"`
}

var nativeFlowZeroAmount = nativeFlowAmount{Value: 0, Offset: 100}

type nativeFlowOrderItem struct {
	Name       string           `json:"name"`
	Amount     nativeFlowAmount `json:"amount"`
	Quantity   int              `json:"quantity"`
	SaleAmount nativeFlowAmount `json:"sale_amount"`
}

type nativeFlowOrder struct {
	Status    string                `json:"status"`
	Subtotal  nativeFlowAmount      `json:"subtotal"`
	OrderType string                `json:"order_type"`
	Items     []nativeFlowOrderItem `json:"items"`
}

type nativeFlowPixStaticCode struct {
	MerchantName string `json:"merchant_name"`
	Key          string `json:"key"`
	KeyType      string `json:"key_type"`
}

type nativeFlowPaymentSetting struct {
	Type          string                  `json:"type"`
	PixStaticCode nativeFlowPixStaticCode `json:"pix_static_code"`
}

type nativeFlowPaymentInfoParams struct {
	Currency           string                     `json:"currency"`
	TotalAmount        nativeFlowAmount           `json:"total_amount"`
	ReferenceID        string                     `json:"reference_id"`
	Type               string                     `json:"type"`
	Order              nativeFlowOrder            `json:"order"`
	PaymentSettings    []nativeFlowPaymentSetting `json:"payment_settings"`
	SharePaymentStatus bool                       `json:"share_payment_status"`
}

// marshalNativeFlowJSON matches JS JSON.stringify: field order kept, no HTML escaping of &, <, >.
func marshalNativeFlowJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// BuildNativeFlowButton builds a native flow button with the same payloads as
// Evolution API (PR #2526, interactiveMessage.helper.ts toNativeFlowButton).
func BuildNativeFlowButton(b InteractiveButton, newID func() string) (*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton, error) {
	if newID == nil {
		newID = uuid.NewString
	}
	var name string
	var params any
	switch b.Kind {
	case ButtonReply:
		id := b.ID
		if id == "" {
			id = newID()
		}
		name = nativeFlowQuickReply
		params = struct {
			DisplayText string `json:"display_text"`
			ID          string `json:"id"`
		}{b.DisplayText, id}
	case ButtonURL:
		merchantURL := b.MerchantURL
		if merchantURL == "" {
			merchantURL = b.URL
		}
		name = nativeFlowCtaURL
		params = struct {
			DisplayText string `json:"display_text"`
			URL         string `json:"url"`
			MerchantURL string `json:"merchant_url"`
		}{b.DisplayText, b.URL, merchantURL}
	case ButtonCall:
		name = nativeFlowCtaCall
		params = struct {
			DisplayText string `json:"display_text"`
			PhoneNumber string `json:"phone_number"`
		}{b.DisplayText, b.PhoneNumber}
	case ButtonCopy:
		name = nativeFlowCtaCopy
		params = struct {
			DisplayText string `json:"display_text"`
			CopyCode    string `json:"copy_code"`
		}{b.DisplayText, b.CopyCode}
	case ButtonPix:
		if b.Pix == nil {
			return nil, interactiveError("pix payment is required")
		}
		currency := b.Pix.Currency
		if currency == "" {
			currency = "BRL"
		}
		name = nativeFlowPaymentInfo
		params = nativeFlowPaymentInfoParams{
			Currency:    currency,
			TotalAmount: nativeFlowZeroAmount,
			ReferenceID: newID(),
			Type:        "physical-goods",
			Order: nativeFlowOrder{
				Status:    "pending",
				Subtotal:  nativeFlowZeroAmount,
				OrderType: "ORDER",
				Items: []nativeFlowOrderItem{{
					Name:       "",
					Amount:     nativeFlowZeroAmount,
					Quantity:   0,
					SaleAmount: nativeFlowZeroAmount,
				}},
			},
			PaymentSettings: []nativeFlowPaymentSetting{{
				Type: "pix_static_code",
				PixStaticCode: nativeFlowPixStaticCode{
					MerchantName: b.Pix.MerchantName,
					Key:          b.Pix.Key,
					KeyType:      b.Pix.KeyType,
				},
			}},
			SharePaymentStatus: false,
		}
	case ButtonRaw:
		return &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String(b.RawName),
			ButtonParamsJSON: proto.String(b.RawParamsJSON),
		}, nil
	default:
		return nil, interactiveError("unsupported button type %d", b.Kind)
	}
	data, err := marshalNativeFlowJSON(params)
	if err != nil {
		return nil, err
	}
	return &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
		Name:             proto.String(name),
		ButtonParamsJSON: proto.String(data),
	}, nil
}

// BuildInteractiveMessage builds a top-level (no ViewOnce) native flow InteractiveMessage.
// nil newID / newSecret default to uuid.NewString / random.Bytes(32).
func BuildInteractiveMessage(
	in *Interactive,
	contextInfo *waE2E.ContextInfo,
	newID func() string,
	newSecret func() []byte,
) (*waE2E.Message, error) {
	if in == nil {
		return nil, interactiveError("message is required")
	}
	if newID == nil {
		newID = uuid.NewString
	}
	if newSecret == nil {
		newSecret = func() []byte { return random.Bytes(32) }
	}

	buttons := make([]*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton, len(in.Buttons))
	for i, b := range in.Buttons {
		btn, err := BuildNativeFlowButton(b, newID)
		if err != nil {
			return nil, err
		}
		buttons[i] = btn
	}
	messageParams, err := marshalNativeFlowJSON(struct {
		From       string `json:"from"`
		TemplateID string `json:"templateId"`
	}{"api", newID()})
	if err != nil {
		return nil, err
	}

	interactive := &waE2E.InteractiveMessage{
		Body: &waE2E.InteractiveMessage_Body{Text: proto.String(in.Body)},
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
				Buttons:           buttons,
				MessageParamsJSON: proto.String(messageParams),
			},
		},
		ContextInfo: contextInfo,
	}
	if in.Footer != "" {
		interactive.Footer = &waE2E.InteractiveMessage_Footer{Text: proto.String(in.Footer)}
	}

	media := in.HeaderMedia
	hasMedia := media != nil && (media.Image != nil || media.Video != nil || media.Document != nil)
	if in.HeaderTitle != "" || in.HeaderSubtitle != "" || hasMedia {
		header := &waE2E.InteractiveMessage_Header{}
		if in.HeaderTitle != "" {
			header.Title = proto.String(in.HeaderTitle)
		}
		if in.HeaderSubtitle != "" {
			header.Subtitle = proto.String(in.HeaderSubtitle)
		}
		if hasMedia {
			header.HasMediaAttachment = proto.Bool(true)
			switch {
			case media.Image != nil:
				header.Media = &waE2E.InteractiveMessage_Header_ImageMessage{ImageMessage: media.Image}
			case media.Video != nil:
				header.Media = &waE2E.InteractiveMessage_Header_VideoMessage{VideoMessage: media.Video}
			case media.Document != nil:
				header.Media = &waE2E.InteractiveMessage_Header_DocumentMessage{DocumentMessage: media.Document}
			}
		}
		interactive.Header = header
	}

	return &waE2E.Message{
		InteractiveMessage: interactive,
		MessageContextInfo: &waE2E.MessageContextInfo{
			MessageSecret: newSecret(),
		},
	}, nil
}

// BuildInteractiveBizNode builds the extra <biz> node required for native flow messages to render.
// PIX (payment_info, always the only button): <biz native_flow_name="payment_info"/>
// Otherwise: <biz><interactive type="native_flow" v="1"><native_flow v="9" name="mixed"/></interactive></biz>
func BuildInteractiveBizNode(msg *waE2E.Message) waBinary.Node {
	buttons := msg.GetInteractiveMessage().GetNativeFlowMessage().GetButtons()
	if len(buttons) > 0 && buttons[0].GetName() == nativeFlowPaymentInfo {
		return waBinary.Node{
			Tag:   "biz",
			Attrs: waBinary.Attrs{bizNativeFlowNameAttr: nativeFlowPaymentInfo},
		}
	}
	return waBinary.Node{
		Tag: "biz",
		Content: []waBinary.Node{{
			Tag: "interactive",
			Attrs: waBinary.Attrs{
				"type": bizInteractiveType,
				"v":    bizInteractiveVersion,
			},
			Content: []waBinary.Node{{
				Tag: "native_flow",
				Attrs: waBinary.Attrs{
					"v":    bizNativeFlowVersion,
					"name": bizNativeFlowName,
				},
			}},
		}},
	}
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
