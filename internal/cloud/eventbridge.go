package cloud

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	"github.com/khyeo1011/ev-chargers/internal/charger"
)

// These must match the event pattern on the EventBridge rule
// (see scripts/aws-setup.sh).
const (
	EventSource              = "ev.chargers"
	DetailTypeChargerCreated = "ChargerCreated"
)

// EventBridgePublisher puts events on an event bus. Rules on the bus decide
// where they go (here: an SQS queue). The API doesn't know or care who
// consumes them, which is the point of an event bus.
type EventBridgePublisher struct {
	Client  *eventbridge.Client
	BusName string
}

func (p *EventBridgePublisher) ChargerCreated(ctx context.Context, c charger.Charger) error {
	detail, err := json.Marshal(c)
	if err != nil {
		return err
	}
	out, err := p.Client.PutEvents(ctx, &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{{
			EventBusName: aws.String(p.BusName),
			Source:       aws.String(EventSource),
			DetailType:   aws.String(DetailTypeChargerCreated),
			Detail:       aws.String(string(detail)),
		}},
	})
	if err != nil {
		return err
	}
	// Gotcha: PutEvents can return no error yet still reject individual
	// entries, so the per-entry failure count has to be checked.
	if out.FailedEntryCount > 0 {
		return fmt.Errorf("eventbridge rejected event: %s %s",
			aws.ToString(out.Entries[0].ErrorCode), aws.ToString(out.Entries[0].ErrorMessage))
	}
	return nil
}
