package gows

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Golden fixtures: the literal JSON produced by toNativeFlowButton from
// https://github.com/evolution-foundation/evolution-api/pull/2526
// (src/api/integrations/channel/whatsapp/helpers/interactiveMessage.helper.ts)
// with generateRandomId -> "REF1".
//
// Divergences from Evolution:
//   - quick_reply: the TS uses `button.id ?? generateRandomId()`, which keeps an empty "" id.
//     proto3 can't tell empty from absent, so here an empty id generates a new one.
//   - payment_info: an empty currency defaults to "BRL" (Evolution has no default).
//   - cta_url: optional merchantUrl field; empty falls back to url (Evolution always uses url).
const (
	goldenQuickReply  = `{"display_text":"Sim","id":"yes"}`
	goldenCtaURL      = `{"display_text":"Site","url":"https://example.com","merchant_url":"https://example.com"}`
	goldenCtaCall     = `{"display_text":"Ligar","phone_number":"+5511999999999"}`
	goldenCtaCopy     = `{"display_text":"Copiar","copy_code":"ABC123"}`
	goldenPaymentInfo = `{"currency":"BRL","total_amount":{"value":0,"offset":100},"reference_id":"REF1","type":"physical-goods","order":{"status":"pending","subtotal":{"value":0,"offset":100},"order_type":"ORDER","items":[{"name":"","amount":{"value":0,"offset":100},"quantity":0,"sale_amount":{"value":0,"offset":100}}]},"payment_settings":[{"type":"pix_static_code","pix_static_code":{"merchant_name":"Loja","key":"KEY","key_type":"EVP"}}],"share_payment_status":false}`
)

func fixedID() string { return "REF1" }

func TestBuildNativeFlowButton_Golden(t *testing.T) {
	pix := func(keyType, currency string) InteractiveButton {
		return InteractiveButton{Kind: ButtonPix, Pix: &PixPayment{MerchantName: "Loja", Key: "KEY", KeyType: keyType, Currency: currency}}
	}
	goldenPix := func(keyType string) string {
		return strings.Replace(goldenPaymentInfo, `"key_type":"EVP"`, `"key_type":"`+keyType+`"`, 1)
	}
	tests := []struct {
		name     string
		button   InteractiveButton
		wantName string
		want     string
	}{
		{"quick_reply", InteractiveButton{Kind: ButtonReply, DisplayText: "Sim", ID: "yes"}, "quick_reply", goldenQuickReply},
		{"quick_reply empty id", InteractiveButton{Kind: ButtonReply, DisplayText: "Sim"}, "quick_reply", `{"display_text":"Sim","id":"REF1"}`},
		{"cta_url default merchant_url", InteractiveButton{Kind: ButtonURL, DisplayText: "Site", URL: "https://example.com"}, "cta_url", goldenCtaURL},
		{"cta_url merchant_url", InteractiveButton{Kind: ButtonURL, DisplayText: "Site", URL: "https://example.com", MerchantURL: "https://m.example.com"}, "cta_url", `{"display_text":"Site","url":"https://example.com","merchant_url":"https://m.example.com"}`},
		{"cta_url with &", InteractiveButton{Kind: ButtonURL, DisplayText: "Site", URL: "https://example.com/?a=1&b=<2>"}, "cta_url", `{"display_text":"Site","url":"https://example.com/?a=1&b=<2>","merchant_url":"https://example.com/?a=1&b=<2>"}`},
		{"cta_call", InteractiveButton{Kind: ButtonCall, DisplayText: "Ligar", PhoneNumber: "+5511999999999"}, "cta_call", goldenCtaCall},
		{"cta_copy", InteractiveButton{Kind: ButtonCopy, DisplayText: "Copiar", CopyCode: "ABC123"}, "cta_copy", goldenCtaCopy},
		{"payment_info default currency", pix("EVP", ""), "payment_info", goldenPaymentInfo},
		{"payment_info BRL", pix("EVP", "BRL"), "payment_info", goldenPaymentInfo},
		{"payment_info PHONE", pix("PHONE", ""), "payment_info", goldenPix("PHONE")},
		{"payment_info EMAIL", pix("EMAIL", ""), "payment_info", goldenPix("EMAIL")},
		{"payment_info CPF", pix("CPF", ""), "payment_info", goldenPix("CPF")},
		{"payment_info CNPJ", pix("CNPJ", ""), "payment_info", goldenPix("CNPJ")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			btn, err := BuildNativeFlowButton(tt.button, fixedID)
			require.NoError(t, err)
			require.Equal(t, tt.wantName, btn.GetName())
			// byte-exact: same field order and no HTML escaping as JS JSON.stringify
			require.Equal(t, tt.want, btn.GetButtonParamsJSON())
		})
	}
}

func TestBuildNativeFlowButton_Raw(t *testing.T) {
	params := `{"any":"thing", "nested":{"x":1}}`
	btn, err := BuildNativeFlowButton(InteractiveButton{Kind: ButtonRaw, RawName: "single_select", RawParamsJSON: params}, fixedID)
	require.NoError(t, err)
	require.Equal(t, "single_select", btn.GetName())
	require.Equal(t, params, btn.GetButtonParamsJSON())
}

func TestBuildInteractiveMessage(t *testing.T) {
	secret := []byte("fixed-secret")
	fixedSecret := func() []byte { return secret }
	replies := []InteractiveButton{{Kind: ButtonReply, DisplayText: "Sim", ID: "yes"}}

	t.Run("minimal", func(t *testing.T) {
		ctx := &waE2E.ContextInfo{StanzaID: proto.String("quoted")}
		msg, err := BuildInteractiveMessage(&Interactive{Body: "hello", Buttons: replies}, ctx, fixedID, fixedSecret)
		require.NoError(t, err)
		require.Nil(t, msg.ViewOnceMessage)
		require.Nil(t, msg.ViewOnceMessageV2)
		require.Nil(t, msg.ViewOnceMessageV2Extension)
		im := msg.GetInteractiveMessage()
		require.NotNil(t, im)
		nf := im.GetNativeFlowMessage()
		require.NotNil(t, nf)
		require.Len(t, nf.GetButtons(), 1)
		require.Equal(t, `{"from":"api","templateId":"REF1"}`, nf.GetMessageParamsJSON())
		require.Equal(t, "hello", im.GetBody().GetText())
		require.Nil(t, im.Footer)
		require.Nil(t, im.Header)
		require.Same(t, ctx, im.GetContextInfo())
		require.Equal(t, secret, msg.GetMessageContextInfo().GetMessageSecret())
	})

	t.Run("footer and title", func(t *testing.T) {
		msg, err := BuildInteractiveMessage(&Interactive{HeaderTitle: "T", HeaderSubtitle: "S", Body: "b", Footer: "f", Buttons: replies}, nil, fixedID, fixedSecret)
		require.NoError(t, err)
		im := msg.GetInteractiveMessage()
		require.Equal(t, "f", im.GetFooter().GetText())
		require.Equal(t, "T", im.GetHeader().GetTitle())
		require.Equal(t, "S", im.GetHeader().GetSubtitle())
		require.False(t, im.GetHeader().GetHasMediaAttachment())
		require.Nil(t, im.GetHeader().GetMedia())
	})

	t.Run("image header", func(t *testing.T) {
		img := &waE2E.ImageMessage{URL: proto.String("https://mmg/img")}
		msg, err := BuildInteractiveMessage(&Interactive{
			Body:           "b",
			Buttons:        replies,
			HasHeaderMedia: true,
			HeaderMedia:    &InteractiveHeaderMedia{Image: img},
		}, nil, fixedID, fixedSecret)
		require.NoError(t, err)
		h := msg.GetInteractiveMessage().GetHeader()
		require.True(t, h.GetHasMediaAttachment())
		require.Same(t, img, h.GetImageMessage())
		require.Nil(t, h.Title)
	})

	t.Run("document and video header", func(t *testing.T) {
		doc := &waE2E.DocumentMessage{FileName: proto.String("a.pdf")}
		msg, err := BuildInteractiveMessage(&Interactive{Body: "b", Buttons: replies, HasHeaderMedia: true, HeaderMedia: &InteractiveHeaderMedia{Document: doc}}, nil, fixedID, fixedSecret)
		require.NoError(t, err)
		require.Same(t, doc, msg.GetInteractiveMessage().GetHeader().GetDocumentMessage())

		vid := &waE2E.VideoMessage{URL: proto.String("https://mmg/vid")}
		msg, err = BuildInteractiveMessage(&Interactive{Body: "b", Buttons: replies, HasHeaderMedia: true, HeaderMedia: &InteractiveHeaderMedia{Video: vid}}, nil, fixedID, fixedSecret)
		require.NoError(t, err)
		require.Same(t, vid, msg.GetInteractiveMessage().GetHeader().GetVideoMessage())
	})

	t.Run("default generators", func(t *testing.T) {
		msg, err := BuildInteractiveMessage(&Interactive{Body: "b", Buttons: replies}, nil, nil, nil)
		require.NoError(t, err)
		require.Len(t, msg.GetMessageContextInfo().GetMessageSecret(), 32)
		require.NotContains(t, msg.GetInteractiveMessage().GetNativeFlowMessage().GetMessageParamsJSON(), `"templateId":""`)
	})
}

func TestBuildInteractiveBizNode(t *testing.T) {
	mixed := waBinary.Node{
		Tag: "biz",
		Content: []waBinary.Node{{
			Tag:   "interactive",
			Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
			Content: []waBinary.Node{{
				Tag:   "native_flow",
				Attrs: waBinary.Attrs{"v": "9", "name": "mixed"},
			}},
		}},
	}
	withButtons := func(names ...string) *waE2E.Message {
		nf := &waE2E.InteractiveMessage_NativeFlowMessage{}
		for _, n := range names {
			nf.Buttons = append(nf.Buttons, &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{Name: proto.String(n)})
		}
		return &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
			InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: nf},
		}}
	}

	require.Equal(t, mixed, BuildInteractiveBizNode(withButtons("quick_reply", "quick_reply")))
	require.Equal(t, mixed, BuildInteractiveBizNode(withButtons("cta_url", "cta_copy")))
	require.Equal(t, mixed, BuildInteractiveBizNode(nil))
	require.Equal(t, waBinary.Node{
		Tag:   "biz",
		Attrs: waBinary.Attrs{"native_flow_name": "payment_info"},
	}, BuildInteractiveBizNode(withButtons("payment_info")))
}

func TestValidateInteractive(t *testing.T) {
	reply := func(id string) InteractiveButton {
		return InteractiveButton{Kind: ButtonReply, DisplayText: "R " + id, ID: id}
	}
	urlBtn := InteractiveButton{Kind: ButtonURL, DisplayText: "Site", URL: "https://example.com"}
	callBtn := InteractiveButton{Kind: ButtonCall, DisplayText: "Ligar", PhoneNumber: "+5511"}
	copyBtn := InteractiveButton{Kind: ButtonCopy, DisplayText: "Copiar", CopyCode: "X"}
	pixBtn := InteractiveButton{Kind: ButtonPix, Pix: &PixPayment{MerchantName: "Loja", Key: "k", KeyType: "EVP"}}
	raw := InteractiveButton{Kind: ButtonRaw, RawName: "single_select", RawParamsJSON: `{}`}
	rawPix := InteractiveButton{Kind: ButtonRaw, RawName: "payment_info", RawParamsJSON: `{}`}
	repeat := func(b InteractiveButton, n int) []InteractiveButton {
		out := make([]InteractiveButton, n)
		for i := range out {
			out[i] = b
		}
		return out
	}

	tests := []struct {
		name     string
		in       *Interactive
		wantErr  string
		wantWarn []string
	}{
		{"valid replies", &Interactive{Body: "b", Buttons: []InteractiveButton{reply("a"), reply("b"), reply("c")}}, "", nil},
		{"V1 empty body", &Interactive{Body: "  ", Buttons: []InteractiveButton{reply("a")}}, "interactive: body is required", nil},
		{"V2 no buttons", &Interactive{Body: "b"}, "interactive: at least one button is required", nil},
		{"V3 no type", &Interactive{Body: "b", Buttons: []InteractiveButton{{DisplayText: "x"}}}, "interactive: button[0] has no type", nil},
		{"V4 no displayText", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonURL, URL: "https://x.com"}}}, "interactive: button[0] displayText is required", nil},
		{"V5 4 replies", &Interactive{Body: "b", Buttons: []InteractiveButton{reply("a"), reply("b"), reply("c"), reply("d")}}, "interactive: quick_reply supports up to 3 buttons", nil},
		{"V6 reply+url", &Interactive{Body: "b", Buttons: []InteractiveButton{reply("a"), urlBtn}}, "interactive: quick_reply cannot be mixed with other button types", nil},
		{"V7 pix+reply", &Interactive{Body: "b", Buttons: []InteractiveButton{pixBtn, reply("a")}}, "interactive: pix button must be the only button", nil},
		{"V7 pix+pix", &Interactive{Body: "b", Buttons: []InteractiveButton{pixBtn, pixBtn}}, "interactive: pix button must be the only button", nil},
		{"V7 pix+raw", &Interactive{Body: "b", Buttons: []InteractiveButton{pixBtn, raw}}, "interactive: pix button must be the only button", nil},
		{"V8 3 cta", &Interactive{Body: "b", Buttons: []InteractiveButton{urlBtn, callBtn, copyBtn}}, "interactive: cta buttons (url/call/copy) support up to 2 buttons", nil},
		{"V8 2 cta + raw valid", &Interactive{Body: "b", Buttons: []InteractiveButton{urlBtn, callBtn, raw}}, "", nil},
		{"V9 pix merchantName", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonPix, Pix: &PixPayment{Key: "k", KeyType: "EVP"}}}}, "interactive: pix merchantName is required", nil},
		{"V9 pix key", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonPix, Pix: &PixPayment{MerchantName: "m", KeyType: "EVP"}}}}, "interactive: pix key is required", nil},
		{"V9 pix keyType", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonPix, Pix: &PixPayment{MerchantName: "m", Key: "k"}}}}, "interactive: pix keyType is required", nil},
		{"V9 pix nil", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonPix}}}, "interactive: pix merchantName is required", nil},
		{"V10 duplicate id", &Interactive{Body: "b", Buttons: []InteractiveButton{reply("a"), reply("a")}}, `interactive: duplicate button id "a"`, nil},
		{"V11 url not http", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonURL, DisplayText: "x", URL: "ftp://x.com"}}}, "interactive: button[0] url must be http(s)", nil},
		{"V12 call phone", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonCall, DisplayText: "x"}}}, "interactive: button[0] phoneNumber is required", nil},
		{"V12 copy code", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonCopy, DisplayText: "x"}}}, "interactive: button[0] copyCode is required", nil},
		{"V13 raw invalid json", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonRaw, RawName: "x", RawParamsJSON: "{"}}}, "interactive: button[0] raw requires name and valid paramsJson", nil},
		{"V13 raw no name", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonRaw, RawParamsJSON: "{}"}}}, "interactive: button[0] raw requires name and valid paramsJson", nil},
		{"V14 11 raw", &Interactive{Body: "b", Buttons: repeat(raw, 11)}, "interactive: too many buttons (max 10)", nil},
		{"raw reply+raw valid", &Interactive{Body: "b", Buttons: []InteractiveButton{reply("a"), raw}}, "", nil},
		{"raw 4 reply+raw V5", &Interactive{Body: "b", Buttons: []InteractiveButton{reply("a"), reply("b"), reply("c"), reply("d"), raw}}, "interactive: quick_reply supports up to 3 buttons", nil},
		{"pix alone valid", &Interactive{Body: "b", Buttons: []InteractiveButton{pixBtn}}, "", nil},
		{"2 cta valid", &Interactive{Body: "b", Buttons: []InteractiveButton{urlBtn, copyBtn}}, "", nil},
		{"raw payment_info alone valid", &Interactive{Body: "b", Buttons: []InteractiveButton{rawPix}}, "", nil},
		{"raw payment_info+url", &Interactive{Body: "b", Buttons: []InteractiveButton{rawPix, urlBtn}}, "interactive: pix button must be the only button", nil},
		{"raw payment_info+reply", &Interactive{Body: "b", Buttons: []InteractiveButton{reply("a"), rawPix}}, "interactive: pix button must be the only button", nil},
		{"V11 merchantUrl not http", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonURL, DisplayText: "x", URL: "https://x.com", MerchantURL: "javascript:alert(1)"}}}, "interactive: button[0] merchantUrl must be http(s)", nil},
		{"V11 merchantUrl valid", &Interactive{Body: "b", Buttons: []InteractiveButton{{Kind: ButtonURL, DisplayText: "x", URL: "https://x.com", MerchantURL: "https://m.x.com"}}}, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := ValidateInteractive(tt.in)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantWarn, warnings)
		})
	}
}

func TestIsRealMessage_Interactive(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.Message
		want bool
	}{
		{"interactive", &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{}}, true},
		{"interactive response", &waE2E.Message{InteractiveResponseMessage: &waE2E.InteractiveResponseMessage{}}, true},
		{"list response", &waE2E.Message{ListResponseMessage: &waE2E.ListResponseMessage{}}, true},
		{"buttons response (phone click)", &waE2E.Message{ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{}}, true},
		{"template button reply (web click)", &waE2E.Message{TemplateButtonReplyMessage: &waE2E.TemplateButtonReplyMessage{}}, true},
		{"protocol", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isRealMessage(&events.Message{Message: tt.msg}))
		})
	}
}

func TestExtractContextInfo_Interactive(t *testing.T) {
	ctx := &waE2E.ContextInfo{StanzaID: proto.String("q")}
	require.Same(t, ctx, ExtractContextInfo(&waE2E.Message{
		InteractiveMessage: &waE2E.InteractiveMessage{ContextInfo: ctx},
	}))
	require.Same(t, ctx, ExtractContextInfo(&waE2E.Message{
		InteractiveResponseMessage: &waE2E.InteractiveResponseMessage{ContextInfo: ctx},
	}))
	require.Same(t, ctx, ExtractContextInfo(&waE2E.Message{
		ListResponseMessage: &waE2E.ListResponseMessage{ContextInfo: ctx},
	}))
	require.Same(t, ctx, ExtractContextInfo(&waE2E.Message{
		ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{ContextInfo: ctx},
	}))
	require.Same(t, ctx, ExtractContextInfo(&waE2E.Message{
		TemplateButtonReplyMessage: &waE2E.TemplateButtonReplyMessage{ContextInfo: ctx},
	}))
}
