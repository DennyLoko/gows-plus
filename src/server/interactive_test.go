package server

import (
	"context"
	"errors"
	"testing"

	"github.com/devlikeapro/gows/gows"
	__ "github.com/devlikeapro/gows/proto"
	"github.com/stretchr/testify/require"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func validInteractive() *__.InteractiveMessage {
	return &__.InteractiveMessage{
		Body: "Choose",
		Buttons: []*__.NativeFlowButton{
			{Button: &__.NativeFlowButton_Reply{Reply: &__.QuickReplyButton{DisplayText: "Yes", Id: "yes"}}},
		},
	}
}

func TestValidateInteractiveRequest(t *testing.T) {
	user := types.NewJID("5511999999999", types.DefaultUserServer)
	withHeaderMedia := func(mt __.MediaType) *__.InteractiveMessage {
		in := validInteractive()
		in.Header = &__.InteractiveHeader{Media: &__.Media{Type: mt, Content: []byte("data")}}
		return in
	}

	cases := []struct {
		name string
		jid  types.JID
		req  *__.MessageRequest
	}{
		{"newsletter", types.NewJID("123", types.NewsletterServer), &__.MessageRequest{}},
		{"status", types.StatusBroadcastJID, &__.MessageRequest{}},
		{"broadcast list", types.NewJID("1234567890", types.BroadcastServer), &__.MessageRequest{}},
		{"media", user, &__.MessageRequest{Media: &__.Media{Type: __.MediaType_IMAGE}}},
		{"text", user, &__.MessageRequest{Text: "hello"}},
		{"list", user, &__.MessageRequest{List: &__.ListMessage{}}},
		{"poll", user, &__.MessageRequest{Poll: &__.PollMessage{}}},
		{"pollVote", user, &__.MessageRequest{PollVote: &__.PollVoteMessage{}}},
		{"event", user, &__.MessageRequest{Event: &__.EventMessage{}}},
		{"location", user, &__.MessageRequest{Location: &__.Location{}}},
		{"contacts", user, &__.MessageRequest{Contacts: []*__.VCardContact{{}}}},
		{"participants", user, &__.MessageRequest{Participants: []string{"5511888888888@s.whatsapp.net"}}},
		{"preview", user, &__.MessageRequest{Preview: &__.LinkPreview{}}},
		{"linkPreview", user, &__.MessageRequest{LinkPreview: true}},
		{"backgroundColor", user, &__.MessageRequest{BackgroundColor: &__.OptionalString{Value: "#ffffff"}}},
		{"font", user, &__.MessageRequest{Font: &__.OptionalUInt32{Value: 1}}},
		{"forward", user, &__.MessageRequest{Forward: &__.ForwardOptions{MessageId: "x"}}},
		{"header audio", user, &__.MessageRequest{Interactive: withHeaderMedia(__.MediaType_AUDIO)}},
		{"header ptv", user, &__.MessageRequest{Interactive: withHeaderMedia(__.MediaType_PTV)}},
		{"header without content", user, &__.MessageRequest{Interactive: &__.InteractiveMessage{
			Body:    "Choose",
			Header:  &__.InteractiveHeader{Media: &__.Media{Type: __.MediaType_IMAGE}},
			Buttons: validInteractive().Buttons,
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.req.Interactive == nil {
				tc.req.Interactive = validInteractive()
			}
			err := validateInteractiveRequest(tc.req, tc.jid)
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}

	t.Run("valid with replyTo, mentions and id", func(t *testing.T) {
		req := &__.MessageRequest{
			Id:          "ID1",
			ReplyTo:     "MSG1",
			Mentions:    []string{"5511888888888@s.whatsapp.net"},
			Interactive: validInteractive(),
		}
		require.NoError(t, validateInteractiveRequest(req, user))
	})

	t.Run("valid with image header", func(t *testing.T) {
		req := &__.MessageRequest{Interactive: withHeaderMedia(__.MediaType_IMAGE)}
		require.NoError(t, validateInteractiveRequest(req, types.NewJID("123-456", types.GroupServer)))
	})
}

func TestToInteractiveDomain(t *testing.T) {
	in := &__.InteractiveMessage{
		Header: &__.InteractiveHeader{Title: proto.String("Title"), Subtitle: proto.String("Sub")},
		Body:   "Body",
		Footer: proto.String("Footer"),
		Buttons: []*__.NativeFlowButton{
			{Button: &__.NativeFlowButton_Reply{Reply: &__.QuickReplyButton{DisplayText: "Yes", Id: "yes"}}},
			{Button: &__.NativeFlowButton_Url{Url: &__.CtaUrlButton{DisplayText: "Site", Url: "https://example.com", MerchantUrl: proto.String("https://m.example.com")}}},
			{Button: &__.NativeFlowButton_Call{Call: &__.CtaCallButton{DisplayText: "Call", PhoneNumber: "+5511999999999"}}},
			{Button: &__.NativeFlowButton_Copy{Copy: &__.CtaCopyButton{DisplayText: "Copy", CopyCode: "CODE"}}},
			{Button: &__.NativeFlowButton_Raw{Raw: &__.NativeFlowRawButton{Name: "custom", ParamsJson: `{"a":1}`}}},
			{},
		},
	}
	out := toInteractiveDomain(in)
	require.Equal(t, &gows.Interactive{
		HeaderTitle:    "Title",
		HeaderSubtitle: "Sub",
		HasHeaderMedia: false,
		Body:           "Body",
		Footer:         "Footer",
		Buttons: []gows.InteractiveButton{
			{Kind: gows.ButtonReply, DisplayText: "Yes", ID: "yes"},
			{Kind: gows.ButtonURL, DisplayText: "Site", URL: "https://example.com", MerchantURL: "https://m.example.com"},
			{Kind: gows.ButtonCall, DisplayText: "Call", PhoneNumber: "+5511999999999"},
			{Kind: gows.ButtonCopy, DisplayText: "Copy", CopyCode: "CODE"},
			{Kind: gows.ButtonRaw, RawName: "custom", RawParamsJSON: `{"a":1}`},
			{},
		},
	}, out)

	t.Run("header media", func(t *testing.T) {
		in := validInteractive()
		in.Header = &__.InteractiveHeader{Media: &__.Media{Type: __.MediaType_IMAGE}}
		out := toInteractiveDomain(in)
		require.True(t, out.HasHeaderMedia)
		require.Nil(t, out.HeaderMedia)
	})

	pixCases := []struct {
		keyType  __.PixKeyType
		expected string
	}{
		{__.PixKeyType_PIX_KEY_TYPE_PHONE, "PHONE"},
		{__.PixKeyType_PIX_KEY_TYPE_EMAIL, "EMAIL"},
		{__.PixKeyType_PIX_KEY_TYPE_CPF, "CPF"},
		{__.PixKeyType_PIX_KEY_TYPE_CNPJ, "CNPJ"},
		{__.PixKeyType_PIX_KEY_TYPE_RANDOM, "EVP"},
		{__.PixKeyType_PIX_KEY_TYPE_UNSPECIFIED, ""},
	}
	for _, tc := range pixCases {
		t.Run("pix "+tc.keyType.String(), func(t *testing.T) {
			in := &__.InteractiveMessage{
				Body: "Pay",
				Buttons: []*__.NativeFlowButton{{Button: &__.NativeFlowButton_Pix{Pix: &__.PixButton{
					MerchantName: "Shop", Key: "key", KeyType: tc.keyType,
				}}}},
			}
			out := toInteractiveDomain(in)
			require.Equal(t, gows.InteractiveButton{Kind: gows.ButtonPix, Pix: &gows.PixPayment{
				MerchantName: "Shop", Key: "key", KeyType: tc.expected,
			}}, out.Buttons[0])
		})
	}

	t.Run("pix explicit currency", func(t *testing.T) {
		in := &__.InteractiveMessage{
			Body:    "Pay",
			Buttons: []*__.NativeFlowButton{{Button: &__.NativeFlowButton_Pix{Pix: &__.PixButton{Currency: "USD"}}}},
		}
		out := toInteractiveDomain(in)
		require.Equal(t, "USD", out.Buttons[0].Pix.Currency)
	})
}

func TestBuildInteractiveSend(t *testing.T) {
	in := toInteractiveDomain(validInteractive())
	msg, nodes, err := buildInteractiveSend(in, nil, func() string { return "REF1" }, func() []byte { return []byte("secret") })
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, gows.BuildInteractiveBizNode(msg), nodes[0])
	require.NotNil(t, msg.InteractiveMessage)
	require.Nil(t, msg.ViewOnceMessage)
}

func TestBuildInteractiveSend_PixBizNode(t *testing.T) {
	in := toInteractiveDomain(&__.InteractiveMessage{
		Body:    "Pay",
		Buttons: []*__.NativeFlowButton{{Button: &__.NativeFlowButton_Pix{Pix: &__.PixButton{MerchantName: "Loja", Key: "k", KeyType: __.PixKeyType_PIX_KEY_TYPE_EMAIL}}}},
	})
	_, nodes, err := buildInteractiveSend(in, nil, func() string { return "REF1" }, func() []byte { return []byte("secret") })
	require.NoError(t, err)
	require.Equal(t, []waBinary.Node{{Tag: "biz", Attrs: waBinary.Attrs{"native_flow_name": "payment_info"}}}, nodes)
}

func TestPrepareInteractive(t *testing.T) {
	ctx := context.Background()
	failUpload := func(t *testing.T) headerUploader {
		return func(ctx context.Context, m *__.Media) (*gows.InteractiveHeaderMedia, error) {
			t.Fatal("uploader must not be called")
			return nil, nil
		}
	}
	withImageHeader := func(m *__.Media) *__.InteractiveMessage {
		in := validInteractive()
		in.Header = &__.InteractiveHeader{Media: m}
		return in
	}

	t.Run("empty body is InvalidArgument", func(t *testing.T) {
		in := validInteractive()
		in.Body = " "
		_, _, err := prepareInteractive(ctx, waLog.Noop, in, nil, failUpload(t))
		require.Error(t, err)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("upload error is not InvalidArgument", func(t *testing.T) {
		upload := func(ctx context.Context, m *__.Media) (*gows.InteractiveHeaderMedia, error) {
			return nil, errors.New("upload failed")
		}
		in := withImageHeader(&__.Media{Type: __.MediaType_IMAGE, Content: []byte("data")})
		_, _, err := prepareInteractive(ctx, waLog.Noop, in, nil, upload)
		require.EqualError(t, err, "upload failed")
		require.NotEqual(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("missing content path is not InvalidArgument", func(t *testing.T) {
		upload := func(ctx context.Context, m *__.Media) (*gows.InteractiveHeaderMedia, error) {
			if err := loadMediaContent(waLog.Noop, m); err != nil {
				return nil, err
			}
			t.Fatal("load must fail")
			return nil, nil
		}
		in := withImageHeader(&__.Media{Type: __.MediaType_IMAGE, ContentPath: t.TempDir() + "/missing.jpg"})
		_, _, err := prepareInteractive(ctx, waLog.Noop, in, nil, upload)
		require.Error(t, err)
		require.NotEqual(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("success", func(t *testing.T) {
		contextInfo := &waE2E.ContextInfo{StanzaID: proto.String("MSG1")}
		image := &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}
		upload := func(ctx context.Context, m *__.Media) (*gows.InteractiveHeaderMedia, error) {
			return &gows.InteractiveHeaderMedia{Image: image}, nil
		}
		in := withImageHeader(&__.Media{Type: __.MediaType_IMAGE, Content: []byte("data")})
		msg, nodes, err := prepareInteractive(ctx, waLog.Noop, in, contextInfo, upload)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.Equal(t, gows.BuildInteractiveBizNode(msg), nodes[0])
		require.Same(t, contextInfo, msg.GetInteractiveMessage().GetContextInfo())
		require.Same(t, image, msg.GetInteractiveMessage().GetHeader().GetImageMessage())
	})

	t.Run("no header media does not upload", func(t *testing.T) {
		msg, nodes, err := prepareInteractive(ctx, waLog.Noop, validInteractive(), nil, failUpload(t))
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.NotNil(t, msg.GetInteractiveMessage())
	})
}
