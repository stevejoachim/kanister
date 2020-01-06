package log

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/luci/go-render/render"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/kanisterio/kanister/pkg/field"
)

// Level describes the current log level.
type Level uint32

const (
	// DebugLevel log level.
	DebugLevel Level = Level(logrus.DebugLevel)
	// InfoLevel log level.
	InfoLevel Level = Level(logrus.InfoLevel)
	// ErrorLevel log level.
	ErrorLevel Level = Level(logrus.ErrorLevel)
)

// OutputSink describes the current output sink.
type OutputSink uint8

// Valid log sinks: stderr or fluentbit
const (
	StderrSink OutputSink = iota
	FluentbitSink
)

// Names of environment variables to configure the logging sink
const (
	LoggingServiceHostEnv = "LOGGING_SVC_SERVICE_HOST"
	LoggingServicePortEnv = "LOGGING_SVC_SERVICE_PORT_LOGGING"
)

type logger struct {
	level Level
	ctx   context.Context
	err   error
}

// common logger implementation used in the library
var log = logrus.New()

// SetOutput sets the output destination.
func SetOutput(sink OutputSink) error {
	switch sink {
	case StderrSink:
		log.SetOutput(os.Stderr)
		return nil
	case FluentbitSink:
		fbitAddr, ok := os.LookupEnv(LoggingServiceHostEnv)
		if !ok {
			return errors.New("Unable to find Fluentbit host address")
		}
		fbitPort, ok := os.LookupEnv(LoggingServicePortEnv)
		if !ok {
			return errors.New("Unable to find Fluentbit logging port")
		}
		hook := NewFluentbitHook(fbitAddr + ":" + fbitPort)
		log.AddHook(hook)
		return nil
	default:
		return errors.New("not implemented")
	}
}

// OutputFormat sets the output data format.
type OutputFormat uint8

const (
	// TextFormat creates a plain text format log entry (not CEE).
	TextFormat OutputFormat = iota
	// JSONFormat create a JSON format log entry.
	JSONFormat
)

// Used as a filter to expand (render) the contents of the fields
type renderFormatter struct {
	formatter logrus.Formatter
}

// SetFormatter sets the output formatter.
func SetFormatter(format OutputFormat) {
	switch format {
	case TextFormat:
		log.SetFormatter(&renderFormatter{
			&logrus.TextFormatter{
				FullTimestamp:   true,
				TimestampFormat: time.RFC3339Nano}})
	case JSONFormat:
		log.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	default:
		panic("not implemented")
	}
}

func formatError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	msg := fmt.Sprintf("%+v", err)
	split := strings.SplitAfter(msg, err.Error())

	if len(split) > 1 {
		return split[0], split[1]
	}
	return split[0], ""
}

func (f *renderFormatter) Format(e *logrus.Entry) ([]byte, error) {
	if e != nil && len(e.Data) > 0 {
		cp := *e
		cp.Buffer = nil
		data := make(logrus.Fields, len(e.Data))

		// Expand / render the fields in the entry
		for k, v := range e.Data {
			switch t := v.(type) {
			case error:
				errmsg, stacktrace := formatError(t)
				data[k] = errmsg
				data["stackTrace"] = stacktrace
			case string, fmt.Stringer:
				data[k] = v
			default:
				if k != "time" && k != "field.time" {
					// Preserve time formatting set in the upstream formatter
					data[k] = render.Render(v)
				} else {
					data[k] = v
				}
			}
		}
		cp.Data = data
		return f.formatter.Format(&cp)
	}
	return f.formatter.Format(e)
}

func init() {
	SetFormatter(TextFormat)
}

func Info() Logger {
	return &logger{
		level: InfoLevel,
	}
}

func Error() Logger {
	return &logger{
		level: ErrorLevel,
	}
}

func Debug() Logger {
	return &logger{
		level: DebugLevel,
	}
}

// Print adds `msg` to the log at `InfoLevel`. It is a wrapper for `Info().Print(msg)`, since this is the most common use case.
func Print(msg string, fields ...field.M) {
	Info().Print(msg, fields...)
}

func WithContext(ctx context.Context) Logger {
	return Info().WithContext(ctx)
}

func WithError(err error) Logger {
	return Info().WithError(err)
}

func (l *logger) Print(msg string, fields ...field.M) {
	logFields := make(logrus.Fields)

	if ctxFields := field.FromContext(l.ctx); ctxFields != nil {
		for _, cf := range ctxFields.Fields() {
			logFields[cf.Key()] = cf.Value()
		}
	}

	for _, f := range fields {
		for k, v := range f {
			logFields[k] = v
		}
	}

	entry := log.WithFields(logFields)
	if l.err != nil {
		entry = entry.WithError(l.err)
	}
	entry.Logln(logrus.Level(l.level), msg)
}

func (l *logger) WithContext(ctx context.Context) Logger {
	l.ctx = ctx
	return l
}

func (l *logger) WithError(err error) Logger {
	l.err = err
	return l
}

// Scrapes fields of interest from the logrus.Entry and converts then into a JSON []byte.
func entryToJSON(entry *logrus.Entry) []byte {
	data := make(logrus.Fields, len(entry.Data)+3)

	data["Message"] = entry.Message
	data["Level"] = entry.Level.String()
	data["Time"] = entry.Time

	for k, v := range entry.Data {
		data[k] = v
	}

	bytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	n := []byte("\n")
	bytes = append(bytes, n...)

	return bytes
}
