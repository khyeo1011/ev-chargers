// Package cloud contains the AWS-backed implementations of charger.Store and
// charger.Publisher.
package cloud

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/khyeo1011/ev-chargers/internal/charger"
)

// DynamoStore stores chargers in a table whose partition key is "id" (string).
type DynamoStore struct {
	Client *dynamodb.Client
	Table  string
}

func (s *DynamoStore) Put(ctx context.Context, c charger.Charger) error {
	item, err := attributevalue.MarshalMap(c)
	if err != nil {
		return fmt.Errorf("marshal charger: %w", err)
	}
	_, err = s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.Table),
		Item:      item,
		// Conditional write: refuse to silently overwrite an existing ID.
		ConditionExpression: aws.String("attribute_not_exists(id)"),
	})
	var exists *types.ConditionalCheckFailedException
	if errors.As(err, &exists) {
		return fmt.Errorf("charger %s already exists", c.ID)
	}
	return err
}

func (s *DynamoStore) Get(ctx context.Context, id string) (charger.Charger, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.Table),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	if err != nil {
		return charger.Charger{}, err
	}
	if out.Item == nil {
		return charger.Charger{}, charger.ErrNotFound
	}
	var c charger.Charger
	if err := attributevalue.UnmarshalMap(out.Item, &c); err != nil {
		return charger.Charger{}, fmt.Errorf("unmarshal charger: %w", err)
	}
	return c, nil
}

// List does a full Scan, following pagination (Scan returns at most 1 MB per
// page). Scans read the whole table, so they get slow and expensive as data
// grows. In production you'd query by key or hand search to OpenSearch.
func (s *DynamoStore) List(ctx context.Context) ([]charger.Charger, error) {
	var all []charger.Charger
	pages := dynamodb.NewScanPaginator(s.Client, &dynamodb.ScanInput{TableName: aws.String(s.Table)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		var batch []charger.Charger
		if err := attributevalue.UnmarshalListOfMaps(page.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal page: %w", err)
		}
		all = append(all, batch...)
	}
	return all, nil
}
