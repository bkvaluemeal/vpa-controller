package prometheus

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type Client interface {
	Query(ctx context.Context, query string) (int64, error)
}

type prometheusClient struct {
	api v1.API
}

func NewClient(address string) (Client, error) {
	client, err := api.NewClient(api.Config{
		Address: address,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating prometheus client: %v", err)
	}

	return &prometheusClient{
		api: v1.NewAPI(client),
	}, nil
}

func (c *prometheusClient) Query(ctx context.Context, query string) (total int64, err error) {
	result, warnings, err := c.api.Query(ctx, query, time.Now())
	if err != nil {
		return 0, fmt.Errorf("error querying prometheus: %v", err)
	}
	if len(warnings) > 0 {
		// Log warnings if needed, but for now we continue
	}

	switch {
	case result.Type() == model.ValVector:
		vector := result.(model.Vector)
		if len(vector) == 0 {
			return 0, fmt.Errorf("query returned no results")
		}
		
		for _, sample := range vector {
			total += int64(sample.Value)
		}
		return total, nil

	default:
		return 0, fmt.Errorf("unexpected result type: %v", result.Type())
	}
}
