package plugin

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"cloud.google.com/go/logging/apiv2/loggingpb"
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"google.golang.org/api/iterator"
	"google.golang.org/genproto/googleapis/cloud/audit"
	loggingtype "google.golang.org/genproto/googleapis/logging/type"
)

func logEntryToProto(e *loggingpb.LogEntry, idMap map[string]string) *apiv1.LogsResponse {
	var http *apiv1.LogsResponse_Http

	if e.HttpRequest != nil {
		http = &apiv1.LogsResponse_Http{
			RequestMethod: e.HttpRequest.RequestMethod,
			RequestUrl:    e.HttpRequest.RequestUrl,
			RequestSize:   e.HttpRequest.RequestSize,
			Status:        e.HttpRequest.Status,
			ResponseSize:  e.HttpRequest.ResponseSize,
			RemoteIp:      e.HttpRequest.RemoteIp,
			ServerIp:      e.HttpRequest.ServerIp,
			UserAgent:     e.HttpRequest.UserAgent,
			Referer:       e.HttpRequest.Referer,
			Latency:       e.HttpRequest.Latency,
			Protocol:      e.HttpRequest.Protocol,
		}
	}

	var severity apiv1.LogSeverity

	switch {
	case e.Severity < loggingtype.LogSeverity_DEBUG:
		severity = apiv1.LogSeverity_LOG_SEVERITY_INFO
	case e.Severity == loggingtype.LogSeverity_DEBUG:
		severity = apiv1.LogSeverity_LOG_SEVERITY_DEBUG
	case e.Severity <= loggingtype.LogSeverity_INFO:
		severity = apiv1.LogSeverity_LOG_SEVERITY_INFO
	case e.Severity <= loggingtype.LogSeverity_NOTICE:
		severity = apiv1.LogSeverity_LOG_SEVERITY_NOTICE
	case e.Severity <= loggingtype.LogSeverity_WARNING:
		severity = apiv1.LogSeverity_LOG_SEVERITY_WARN
	default:
		severity = apiv1.LogSeverity_LOG_SEVERITY_ERROR
	}

	var typ apiv1.LogsResponse_Type

	switch {
	case strings.HasSuffix(e.LogName, "%2Fstdout"):
		typ = apiv1.LogsResponse_TYPE_STDOUT
	case strings.HasSuffix(e.LogName, "%2Fstderr") || strings.HasSuffix(e.LogName, "%2Fvarlog%2Fsystem"):
		typ = apiv1.LogsResponse_TYPE_STDERR
	}

	var src string

	switch e.Resource.Type {
	case "cloud_run_revision":
		src = idMap[e.Resource.Labels["service_name"]]
	case "cloudsql_database":
		src = idMap[e.Resource.Labels["database_id"]]
	}

	ret := &apiv1.LogsResponse{
		Source:   src,
		Type:     typ,
		Time:     e.Timestamp,
		Severity: severity,
		Http:     http,
	}

	switch p := e.Payload.(type) {
	case *loggingpb.LogEntry_ProtoPayload:
		var msg audit.AuditLog

		err := p.ProtoPayload.UnmarshalTo(&msg)
		if err != nil {
			panic(err)
		}

		if msg.Status != nil {
			ret.Payload = &apiv1.LogsResponse_Text{
				Text: msg.Status.Message,
			}
		} else {
			ret.Payload = &apiv1.LogsResponse_Text{
				Text: msg.MethodName,
			}
		}

	case *loggingpb.LogEntry_TextPayload:
		ret.Payload = &apiv1.LogsResponse_Text{
			Text: p.TextPayload,
		}

	case *loggingpb.LogEntry_JsonPayload:
		ret.Payload = &apiv1.LogsResponse_Json{
			Json: p.JsonPayload,
		}
	}

	return ret
}

func (p *Plugin) createLogFilter(r *apiv1.LogsRequest) (filter string, idMap map[string]string, err error) {
	var (
		filterOrs          []string
		filterAnds         []string
		cloudRunNames      []string
		cloudFunctionNames []string
		cloudSQLNames      []string
	)

	idMap = make(map[string]string)
	reg := registry.NewRegistry(nil)

	gcp.RegisterTypes(reg)
	_ = reg.Load(r.State.Registry)

	for _, app := range r.Apps {
		gcpID := gcp.ID(p.env, app.Id)
		idMap[gcpID] = app.Id

		if app.Type == deploy.AppTypeFunction {
			cloudFunctionNames = append(cloudFunctionNames, gcpID)
		} else {
			cloudRunNames = append(cloudRunNames, gcpID)
		}
	}

	for _, dep := range r.Dependencies {
		switch dep.Type {
		case deploy.DepTypePostgreSQL, deploy.DepTypeMySQL:
			db := &gcp.CloudSQL{}

			if reg.GetDependencyResource(dep, "cloud_sql", db) {
				gcpID := fmt.Sprintf("%s:%s", p.settings.ProjectID, db.Name.Any())
				cloudSQLNames = append(cloudSQLNames, gcpID)
				idMap[gcpID] = dep.Id
			}
		}
	}

	if len(cloudRunNames) > 0 {
		filterOrs = append(filterOrs, fmt.Sprintf(`(resource.type = "cloud_run_revision" resource.labels.service_name = ("%s"))`, strings.Join(cloudRunNames, `" OR "`))) //nolint:gocritic
	}

	if len(cloudFunctionNames) > 0 {
		filterOrs = append(filterOrs, fmt.Sprintf(`(resource.type = "cloud_function" resource.labels.function_name = ("%s"))`, strings.Join(cloudFunctionNames, `" OR "`))) //nolint:gocritic
	}

	if len(cloudSQLNames) > 0 {
		filterOrs = append(filterOrs, fmt.Sprintf(`(resource.type = "cloudsql_database" resource.labels.database_id = ("%s"))`, strings.Join(cloudSQLNames, `" OR "`))) //nolint:gocritic
	}

	if len(filterOrs) == 0 {
		return "", nil, fmt.Errorf("no valid apps and/or dependencies defined")
	}

	filterAnds = append(filterAnds, fmt.Sprintf("(%s)", strings.Join(filterOrs, " OR ")))

	if r.Severity > apiv1.LogSeverity_LOG_SEVERITY_UNSPECIFIED {
		filterAnds = append(filterAnds, fmt.Sprintf(`severity >= "%s"`, r.Severity.String()[len("LOG_SEVERITY_"):])) //nolint:gocritic
	}

	if r.Start.IsValid() {
		filterAnds = append(filterAnds, fmt.Sprintf(`timestamp >= "%s"`, r.Start.AsTime().Format(time.RFC3339))) //nolint:gocritic
	}

	if r.End.IsValid() && !r.End.AsTime().IsZero() {
		filterAnds = append(filterAnds, fmt.Sprintf(`timestamp <= "%s"`, r.End.AsTime().Format(time.RFC3339))) //nolint:gocritic
	}

	for _, c := range r.Contains {
		filterAnds = append(filterAnds, fmt.Sprintf(`"%s"`, c)) //nolint:gocritic
	}

	for _, c := range r.NotContains {
		filterAnds = append(filterAnds, fmt.Sprintf(`NOT "%s"`, c)) //nolint:gocritic
	}

	if r.Filter != "" {
		filterAnds = append(filterAnds, r.Filter)
	}

	return strings.Join(filterAnds, " "), idMap, nil
}

func (p *Plugin) Logs(r *apiv1.LogsRequest, srv apiv1.LogsPluginService_LogsServer) error {
	ctx := srv.Context()

	loggingCli, err := config.NewGCPLoggingClient(ctx, p.gcred)
	if err != nil {
		return fmt.Errorf("error creating gcp logging client: %w", err)
	}

	filter, idMap, err := p.createLogFilter(r)
	if err != nil {
		return err
	}

	loggingURL := fmt.Sprintf("https://console.cloud.google.com/logs/query;query=%s?project=%s",
		strings.ReplaceAll(url.PathEscape(filter), "=", "%3D"), p.settings.ProjectID)

	p.log.Infof("Logs Explorer Web UI: %s\n", loggingURL)

	iter := loggingCli.ListLogEntries(ctx, &loggingpb.ListLogEntriesRequest{
		ResourceNames: []string{
			"projects/" + p.settings.ProjectID,
		},
		Filter:   filter,
		PageSize: 1000,
	})

	for {
		entry, err := iter.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}

			return fmt.Errorf("getting logs error: %w", err)
		}

		err = srv.Send(logEntryToProto(entry, idMap))
		if err != nil {
			return fmt.Errorf("error sending log entry: %w", err)
		}
	}

	if !r.Follow {
		return nil
	}

	stream, err := loggingCli.TailLogEntries(ctx)
	if err != nil {
		return fmt.Errorf("tailing logs error: %w", err)
	}

	req := &loggingpb.TailLogEntriesRequest{
		ResourceNames: []string{
			"projects/" + p.settings.ProjectID,
		},
		Filter: filter,
	}

	if err := stream.Send(req); err != nil {
		return fmt.Errorf("sending logs request error: %w", err)
	}

	defer stream.CloseSend() //nolint:errcheck

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return err
		}

		if err != nil {
			return fmt.Errorf("receiving logs error: %w", err)
		}

		for _, entry := range resp.Entries {
			err = srv.Send(logEntryToProto(entry, idMap))
			if err != nil {
				return fmt.Errorf("error sending log entry: %w", err)
			}
		}
	}
}
