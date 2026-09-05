package newapi

import (
	"context"
	"errors"

	"github.com/evepupil/ManyRouter/internal/application/collection"
)

type collectionLogReader struct {
	client *Client
}

func (f Factory) NewLogReader(baseURL string, accessToken []byte, adminUserID int64) (collection.LogReader, error) {
	gateway, err := f.NewForSite(baseURL, accessToken, adminUserID)
	if err != nil {
		return nil, err
	}
	reader, ok := gateway.(*Client)
	if !ok {
		return nil, errors.New("New API log reader is unavailable")
	}
	return &collectionLogReader{client: reader}, nil
}

func (reader *collectionLogReader) Read(
	ctx context.Context,
	logType int,
	startTimestamp int64,
	endTimestamp int64,
	page int,
	pageSize int,
) (collection.RemotePage, error) {
	result, err := reader.client.ReadAdminLogs(ctx, logType, startTimestamp, endTimestamp, page, pageSize)
	if err != nil {
		return collection.RemotePage{}, err
	}
	items := make([]collection.RemoteLog, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, collection.RemoteLog{
			ID: item.ID, CreatedAt: item.CreatedAt, Type: item.Type, Model: item.Model,
			InputTokens: item.InputTokens, OutputTokens: item.OutputTokens,
			DurationSeconds: item.DurationSeconds, Stream: item.Stream,
			ChannelID: item.ChannelID, Group: item.Group, RequestID: item.RequestID,
			UpstreamRequestID: item.UpstreamRequestID, ErrorText: item.Content, Other: item.Other,
		})
	}
	return collection.RemotePage{
		Items: items, Total: result.Total, Page: result.Page, PageSize: result.PageSize,
	}, nil
}

var _ collection.LogReaderFactory = Factory{}
