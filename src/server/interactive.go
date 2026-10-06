package server

import (
	"context"

	"github.com/devlikeapro/gows/gows"
	__ "github.com/devlikeapro/gows/proto"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validateInteractiveRequest checks request-level rules for interactive messages (R1, R2, R3).
// Button-level rules live in gows.ValidateInteractive.
func validateInteractiveRequest(req *__.MessageRequest, jid types.JID) error {
	switch {
	case jid.Server == types.NewsletterServer:
		return status.Error(codes.InvalidArgument, "interactive: interactive messages are not supported for newsletter")
	case jid.User == "status" && jid.Server == types.BroadcastServer:
		return status.Error(codes.InvalidArgument, "interactive: interactive messages are not supported for status")
	case jid.Server == types.BroadcastServer:
		return status.Error(codes.InvalidArgument, "interactive: interactive messages are not supported for broadcast list")
	}

	combined := []struct {
		name string
		set  bool
	}{
		{"media", req.GetMedia() != nil},
		{"text", req.GetText() != ""},
		{"list", req.GetList() != nil},
		{"poll", req.GetPoll() != nil},
		{"pollVote", req.GetPollVote() != nil},
		{"event", req.GetEvent() != nil},
		{"location", req.GetLocation() != nil},
		{"contacts", len(req.GetContacts()) > 0},
		{"participants", len(req.GetParticipants()) > 0},
		{"preview", req.GetPreview() != nil},
		{"linkPreview", req.GetLinkPreview()},
		{"backgroundColor", req.GetBackgroundColor() != nil},
		{"font", req.GetFont() != nil},
		{"forward", req.GetForward() != nil},
	}
	for _, c := range combined {
		if c.set {
			return status.Errorf(codes.InvalidArgument, "interactive: interactive cannot be combined with %s", c.name)
		}
	}

	if m := req.GetInteractive().GetHeader().GetMedia(); m != nil {
		switch m.GetType() {
		case __.MediaType_IMAGE, __.MediaType_VIDEO, __.MediaType_DOCUMENT:
		default:
			return status.Error(codes.InvalidArgument, "interactive: header media must be IMAGE, VIDEO or DOCUMENT")
		}
		if len(m.GetContent()) == 0 && m.GetContentPath() == "" {
			return status.Error(codes.InvalidArgument, "interactive: header media requires content or contentPath")
		}
	}
	return nil
}

var pixKeyTypes = map[__.PixKeyType]string{
	__.PixKeyType_PIX_KEY_TYPE_PHONE:  "PHONE",
	__.PixKeyType_PIX_KEY_TYPE_EMAIL:  "EMAIL",
	__.PixKeyType_PIX_KEY_TYPE_CPF:    "CPF",
	__.PixKeyType_PIX_KEY_TYPE_CNPJ:   "CNPJ",
	__.PixKeyType_PIX_KEY_TYPE_RANDOM: "EVP",
}

// toInteractiveDomain converts the proto request into the gows domain. It does not upload header media.
// A button without a variant is kept as a zero InteractiveButton so gows.ValidateInteractive reports it (V3).
func toInteractiveDomain(in *__.InteractiveMessage) *gows.Interactive {
	out := &gows.Interactive{
		HeaderTitle:    in.GetHeader().GetTitle(),
		HeaderSubtitle: in.GetHeader().GetSubtitle(),
		HasHeaderMedia: in.GetHeader().GetMedia() != nil,
		Body:           in.GetBody(),
		Footer:         in.GetFooter(),
		Buttons:        make([]gows.InteractiveButton, 0, len(in.GetButtons())),
	}
	for _, b := range in.GetButtons() {
		var btn gows.InteractiveButton
		switch v := b.GetButton().(type) {
		case *__.NativeFlowButton_Reply:
			btn = gows.InteractiveButton{Kind: gows.ButtonReply, DisplayText: v.Reply.GetDisplayText(), ID: v.Reply.GetId()}
		case *__.NativeFlowButton_Url:
			btn = gows.InteractiveButton{Kind: gows.ButtonURL, DisplayText: v.Url.GetDisplayText(), URL: v.Url.GetUrl(), MerchantURL: v.Url.GetMerchantUrl()}
		case *__.NativeFlowButton_Call:
			btn = gows.InteractiveButton{Kind: gows.ButtonCall, DisplayText: v.Call.GetDisplayText(), PhoneNumber: v.Call.GetPhoneNumber()}
		case *__.NativeFlowButton_Copy:
			btn = gows.InteractiveButton{Kind: gows.ButtonCopy, DisplayText: v.Copy.GetDisplayText(), CopyCode: v.Copy.GetCopyCode()}
		case *__.NativeFlowButton_Pix:
			btn = gows.InteractiveButton{Kind: gows.ButtonPix, Pix: &gows.PixPayment{
				MerchantName: v.Pix.GetMerchantName(),
				Key:          v.Pix.GetKey(),
				KeyType:      pixKeyTypes[v.Pix.GetKeyType()],
				Currency:     v.Pix.GetCurrency(),
			}}
		case *__.NativeFlowButton_Raw:
			btn = gows.InteractiveButton{Kind: gows.ButtonRaw, RawName: v.Raw.GetName(), RawParamsJSON: v.Raw.GetParamsJson()}
		}
		out.Buttons = append(out.Buttons, btn)
	}
	return out
}

// buildInteractiveSend builds the interactive message and the <biz> node required to deliver it.
func buildInteractiveSend(
	in *gows.Interactive,
	contextInfo *waE2E.ContextInfo,
	newID func() string,
	newSecret func() []byte,
) (*waE2E.Message, []waBinary.Node, error) {
	msg, err := gows.BuildInteractiveMessage(in, contextInfo, newID, newSecret)
	if err != nil {
		return nil, nil, err
	}
	return msg, []waBinary.Node{gows.BuildInteractiveBizNode(msg)}, nil
}

// headerUploader loads and uploads the interactive header media.
// Upload errors are returned as-is (never InvalidArgument); the InvalidArgument
// for an unsupported media type is a defensive guard, validateInteractiveRequest
// already rejects it.
type headerUploader func(ctx context.Context, m *__.Media) (*gows.InteractiveHeaderMedia, error)

func newHeaderUploader(cli *gows.GoWS, jid types.JID) headerUploader {
	return func(ctx context.Context, m *__.Media) (*gows.InteractiveHeaderMedia, error) {
		if err := loadMediaContent(cli.Log, m); err != nil {
			return nil, err
		}
		headerMedia := &gows.InteractiveHeaderMedia{}
		var err error
		// Header caption is empty: the text goes in the body
		switch m.GetType() {
		case __.MediaType_IMAGE:
			headerMedia.Image, _, err = uploadImage(ctx, cli, jid, m, "")
		case __.MediaType_VIDEO:
			headerMedia.Video, _, err = uploadVideo(ctx, cli, jid, m, "")
		case __.MediaType_DOCUMENT:
			headerMedia.Document, _, err = uploadDocument(ctx, cli, jid, m, "")
		default:
			return nil, status.Errorf(codes.InvalidArgument, "interactive: unsupported header media type %s", m.GetType())
		}
		if err != nil {
			return nil, err
		}
		return headerMedia, nil
	}
}

// prepareInteractive validates the interactive message, uploads the header media (if any)
// and builds the message with its <biz> node. The request must already pass validateInteractiveRequest.
func prepareInteractive(
	ctx context.Context,
	log waLog.Logger,
	req *__.InteractiveMessage,
	contextInfo *waE2E.ContextInfo,
	upload headerUploader,
) (*waE2E.Message, []waBinary.Node, error) {
	in := toInteractiveDomain(req)
	warnings, err := gows.ValidateInteractive(in)
	if err != nil {
		return nil, nil, status.Error(codes.InvalidArgument, err.Error())
	}
	for _, warning := range warnings {
		log.Warnf("%s", warning)
	}

	if in.HasHeaderMedia {
		in.HeaderMedia, err = upload(ctx, req.GetHeader().GetMedia())
		if err != nil {
			return nil, nil, err
		}
	}

	return buildInteractiveSend(in, contextInfo, nil, nil)
}
