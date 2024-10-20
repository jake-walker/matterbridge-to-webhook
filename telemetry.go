package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Metrics struct {
	messageReceived  metric.Int64Counter
	messageForwarded metric.Int64Counter
	messageDropped   metric.Int64Counter
	processingError  metric.Int64Counter
}

func setupOTelSdk(ctx context.Context) (shutdown func(context.Context) error, err error) {
	var shutdownFuncs []func(context.Context) error

	shutdown = func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}

	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("matterbridge-to-webhook"),
		semconv.ServiceVersion("1.0.0"),
	)

	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	meterProvider, err := newMeterProvider(res)
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	loggerProvider, err := newLoggerProvider(res)
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newMeterProvider(res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	metricExporter, err := otlpmetrichttp.New(context.Background())
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(3*time.Second)),
		),
	)

	return meterProvider, nil
}

func newLoggerProvider(res *resource.Resource) (*log.LoggerProvider, error) {
	logExporter, err := otlploghttp.New(context.Background())
	if err != nil {
		return nil, err
	}

	loggerProvider := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
	)
	return loggerProvider, nil
}

func initMetrics(meter metric.Meter) (Metrics, error) {
	m := Metrics{}

	var err1, err2, err3, err4 error

	m.messageReceived, err1 = meter.Int64Counter(
		"messages_received_total",
		metric.WithDescription("Total number of messages received"),
	)
	m.messageForwarded, err2 = meter.Int64Counter(
		"messages_forwarded_total",
		metric.WithDescription("Total number of messages forwarded to the webhook"),
	)
	m.messageDropped, err3 = meter.Int64Counter(
		"messages_dropped_total",
		metric.WithDescription("Total number of messages not eligable for forwarding"),
	)
	m.processingError, err4 = meter.Int64Counter(
		"processing_errors_total",
		metric.WithDescription("Total number of processing errors"),
	)

	for _, err := range []error{err1, err2, err3, err4} {
		if err != nil {
			return m, fmt.Errorf("failed to create metric: %v", err)
		}
	}

	return m, nil
}
