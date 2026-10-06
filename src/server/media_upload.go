package server

import (
	"context"
	"fmt"
	"os"

	"github.com/devlikeapro/gows/gows"
	"github.com/devlikeapro/gows/media"
	__ "github.com/devlikeapro/gows/proto"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// loadMediaContent reads m.ContentPath (if set) into m.Content
func loadMediaContent(log waLog.Logger, m *__.Media) error {
	if m.GetContentPath() == "" {
		return nil
	}
	content, err := os.ReadFile(m.GetContentPath())
	if err != nil {
		log.Errorf("Failed to read media from '%s': %v", m.GetContentPath(), err)
		return fmt.Errorf("failed to read media from file: %w", err)
	}
	m.Content = content
	return nil
}

func uploadImage(ctx context.Context, cli *gows.GoWS, jid types.JID, m *__.Media, caption string) (*waE2E.ImageMessage, whatsmeow.UploadResponse, error) {
	// Upload
	mediaType := whatsmeow.MediaImage
	mediaResponse, err := cli.UploadMedia(ctx, jid, m.Content, mediaType)
	if err != nil {
		return nil, mediaResponse, err
	}

	// Generate Thumbnail
	thumbnail, err := media.ImageThumbnail(m.Content)
	if err != nil {
		cli.Log.Errorf("Failed to generate thumbnail: %v", err)
	}

	// Get image dimensions
	imgSize, err := media.CurrentSize(m.Content)
	if err != nil {
		cli.Log.Errorf("Failed to get image dimensions: %v", err)
	}

	// Attach
	imageMessage := &waE2E.ImageMessage{
		Caption:       proto.String(caption),
		Mimetype:      proto.String(m.Mimetype),
		JPEGThumbnail: thumbnail,
		Height:        proto.Uint32(imgSize.Height),
		Width:         proto.Uint32(imgSize.Width),
		URL:           &mediaResponse.URL,
		DirectPath:    &mediaResponse.DirectPath,
		FileSHA256:    mediaResponse.FileSHA256,
		FileLength:    &mediaResponse.FileLength,
		MediaKey:      mediaResponse.MediaKey,
		FileEncSHA256: mediaResponse.FileEncSHA256,
	}
	return imageMessage, mediaResponse, nil
}

func uploadVideo(ctx context.Context, cli *gows.GoWS, jid types.JID, m *__.Media, caption string) (*waE2E.VideoMessage, whatsmeow.UploadResponse, error) {
	// NOTE: Keep MediaType_VIDEO and MediaType_PTV in sync
	mediaType := whatsmeow.MediaVideo
	var durationSeconds *uint32
	if m.Video != nil && m.Video.Duration != 0 {
		seconds := uint32(m.Video.Duration)
		durationSeconds = &seconds
	}
	// Upload
	mediaResponse, err := cli.UploadMedia(ctx, jid, m.Content, mediaType)
	if err != nil {
		return nil, mediaResponse, err
	}

	// Generate Thumbnail
	thumbnail, err := media.VideoThumbnail(
		m.Content,
		0,
		struct{ Width int }{Width: 72},
	)

	if err != nil {
		cli.Log.Infof("Failed to generate video thumbnail: %v", err)
	}

	videoMessage := &waE2E.VideoMessage{
		Caption:       proto.String(caption),
		Mimetype:      proto.String(m.Mimetype),
		URL:           &mediaResponse.URL,
		DirectPath:    &mediaResponse.DirectPath,
		MediaKey:      mediaResponse.MediaKey,
		FileEncSHA256: mediaResponse.FileEncSHA256,
		FileSHA256:    mediaResponse.FileSHA256,
		FileLength:    &mediaResponse.FileLength,
		Seconds:       durationSeconds,
		JPEGThumbnail: thumbnail,
	}
	return videoMessage, mediaResponse, nil
}

func uploadDocument(ctx context.Context, cli *gows.GoWS, jid types.JID, m *__.Media, caption string) (*waE2E.DocumentMessage, whatsmeow.UploadResponse, error) {
	mediaType := whatsmeow.MediaDocument
	// Upload
	mediaResponse, err := cli.UploadMedia(ctx, jid, m.Content, mediaType)
	if err != nil {
		return nil, mediaResponse, err
	}

	// Generate Thumbnail if possible
	thumbnail, err := media.ImageThumbnail(m.Content)
	if err != nil {
		cli.Log.Infof("Failed to generate thumbnail: %v", err)
	}

	// Attach
	fileName := m.Filename
	if fileName == "" {
		fileName = "Untitled"
	}
	documentMessage := &waE2E.DocumentMessage{
		Caption:       proto.String(caption),
		Title:         proto.String(fileName),
		Mimetype:      proto.String(m.Mimetype),
		URL:           proto.String(mediaResponse.URL),
		DirectPath:    proto.String(mediaResponse.DirectPath),
		MediaKey:      mediaResponse.MediaKey,
		FileEncSHA256: mediaResponse.FileEncSHA256,
		FileSHA256:    mediaResponse.FileSHA256,
		FileLength:    proto.Uint64(mediaResponse.FileLength),
		FileName:      proto.String(fileName),
		JPEGThumbnail: thumbnail,
	}
	return documentMessage, mediaResponse, nil
}
