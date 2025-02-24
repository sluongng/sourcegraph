package graphqlbackend

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/inconshreveable/log15"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/backend"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/envvar"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/internal/usagestatsdeprecated"
	"github.com/sourcegraph/sourcegraph/internal/actor"
	"github.com/sourcegraph/sourcegraph/internal/conf"
	"github.com/sourcegraph/sourcegraph/internal/featureflag"
	"github.com/sourcegraph/sourcegraph/internal/trace"
	"github.com/sourcegraph/sourcegraph/internal/types"
	"github.com/sourcegraph/sourcegraph/internal/usagestats"
)

func (r *UserResolver) UsageStatistics(ctx context.Context) (*userUsageStatisticsResolver, error) {
	if envvar.SourcegraphDotComMode() {
		if err := backend.CheckSiteAdminOrSameUser(ctx, r.db, r.user.ID); err != nil {
			return nil, err
		}
	}

	stats, err := usagestatsdeprecated.GetByUserID(r.user.ID)
	if err != nil {
		return nil, err
	}
	return &userUsageStatisticsResolver{stats}, nil
}

type userUsageStatisticsResolver struct {
	userUsageStatistics *types.UserUsageStatistics
}

func (s *userUsageStatisticsResolver) PageViews() int32 { return s.userUsageStatistics.PageViews }

func (s *userUsageStatisticsResolver) SearchQueries() int32 {
	return s.userUsageStatistics.SearchQueries
}

func (s *userUsageStatisticsResolver) CodeIntelligenceActions() int32 {
	return s.userUsageStatistics.CodeIntelligenceActions
}

func (s *userUsageStatisticsResolver) FindReferencesActions() int32 {
	return s.userUsageStatistics.FindReferencesActions
}

func (s *userUsageStatisticsResolver) LastActiveTime() *string {
	if s.userUsageStatistics.LastActiveTime != nil {
		t := s.userUsageStatistics.LastActiveTime.Format(time.RFC3339)
		return &t
	}
	return nil
}

func (s *userUsageStatisticsResolver) LastActiveCodeHostIntegrationTime() *string {
	if s.userUsageStatistics.LastCodeHostIntegrationTime != nil {
		t := s.userUsageStatistics.LastCodeHostIntegrationTime.Format(time.RFC3339)
		return &t
	}
	return nil
}

func (*schemaResolver) LogUserEvent(ctx context.Context, args *struct {
	Event        string
	UserCookieID string
}) (*EmptyResponse, error) {
	actor := actor.FromContext(ctx)
	return nil, usagestatsdeprecated.LogActivity(actor.IsAuthenticated(), actor.UID, args.UserCookieID, args.Event)
}

func (r *schemaResolver) LogEvent(ctx context.Context, args *struct {
	Event          string
	UserCookieID   string
	FirstSourceURL *string
	URL            string
	Source         string
	Argument       *string
	CohortID       *string
	Referrer       *string
	PublicArgument *string
	UserProperties *string
	DeviceID       *string
	InsertID       *string
	EventID        *int32
}) (*EmptyResponse, error) {
	if !conf.EventLoggingEnabled() {
		return nil, nil
	}

	var argumentPayload json.RawMessage
	if args.Argument != nil {
		if err := json.Unmarshal([]byte(*args.Argument), &argumentPayload); err != nil {
			return nil, err
		}
	}

	if strings.HasPrefix(args.Event, "search.latencies.frontend.") {
		if err := exportPrometheusSearchLatencies(args.Event, argumentPayload); err != nil {
			log15.Error("export prometheus search latencies", "error", err)
		}
		return nil, nil // Future(slimsag): implement actual event logging for these events
	}

	var publicArgumentPayload json.RawMessage
	if args.PublicArgument != nil {
		if err := json.Unmarshal([]byte(*args.PublicArgument), &publicArgumentPayload); err != nil {
			return nil, err
		}
	}

	var userPropertiesPayload json.RawMessage
	if args.UserProperties != nil {
		if err := json.Unmarshal([]byte(*args.UserProperties), &userPropertiesPayload); err != nil {
			return nil, err
		}
	}

	actor := actor.FromContext(ctx)
	ffs := featureflag.FromContext(ctx)
	return nil, usagestats.LogEvent(ctx, r.db, usagestats.Event{
		EventName:      args.Event,
		URL:            args.URL,
		UserID:         actor.UID,
		UserCookieID:   args.UserCookieID,
		FirstSourceURL: args.FirstSourceURL,
		Source:         args.Source,
		Argument:       argumentPayload,
		FeatureFlags:   ffs,
		CohortID:       args.CohortID,
		Referrer:       args.Referrer,
		PublicArgument: publicArgumentPayload,
		UserProperties: userPropertiesPayload,
		DeviceID:       args.DeviceID,
		EventID:        args.EventID,
		InsertID:       args.InsertID,
	})
}

var (
	searchLatenciesFrontendCodeLoad = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "src_search_latency_frontend_code_load_seconds",
		Help:    "Milliseconds the webapp frontend spends waiting for search result code snippets to load.",
		Buckets: trace.UserLatencyBuckets,
	}, nil)
	searchLatenciesFrontendFirstResult = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "src_search_latency_frontend_first_result_seconds",
		Help:    "Milliseconds the webapp frontend spends waiting for the first search result to load.",
		Buckets: trace.UserLatencyBuckets,
	}, []string{"type"})
)

// exportPrometheusSearchLatencies exports Prometheus search latency metrics given a GraphQL
// LogEvent payload.
func exportPrometheusSearchLatencies(event string, payload json.RawMessage) error {
	var v struct {
		DurationMS float64 `json:"durationMs"`
	}
	if err := json.Unmarshal([]byte(payload), &v); err != nil {
		return err
	}
	if event == "search.latencies.frontend.code-load" {
		searchLatenciesFrontendCodeLoad.WithLabelValues().Observe(v.DurationMS / 1000.0)
	}
	if strings.HasPrefix(event, "search.latencies.frontend.") && strings.HasSuffix(event, ".first-result") {
		searchType := strings.TrimSuffix(strings.TrimPrefix(event, "search.latencies.frontend."), ".first-result")
		searchLatenciesFrontendFirstResult.WithLabelValues(searchType).Observe(v.DurationMS / 1000.0)
	}
	return nil
}
