package util

import "log"

// Log levels
const (
	// LevelError only
	LevelError = 1
	// LevelWarning and error
	LevelWarning = 2
	// LevelInfo, warning and error
	LevelInfo = 3
	// All
	LevelTrace = 4
)

// raft logger and log level
var logger = log.New(log.Writer(), log.Prefix(), log.Flags())
var logLevel = LevelInfo

// SetLogLevel sets log level
func SetLogLevel(level int) {
	if level < LevelError {
		level = LevelError
	}
	if level > LevelTrace {
		level = LevelTrace
	}

	logLevel = level
}

// WriteLog writes an log entry if its level is lower than logLevel, otherwise it's ignored
func WriteLog(level int, format string, v ...interface{}) {
	if level <= logLevel {
		logger.Printf(format, v...)
	}
}

// WriteError writes an error log
func WriteError(format string, v ...interface{}) {
	WriteLog(LevelError, format, v...)
}

// WriteWarning writes a warning log
func WriteWarning(format string, v ...interface{}) {
	WriteLog(LevelWarning, format, v...)
}

// WriteInfo writes a information
func WriteInfo(format string, v ...interface{}) {
	WriteLog(LevelInfo, format, v...)
}

// WriteTrace writes traces and debug information
func WriteTrace(format string, v ...interface{}) {
	WriteLog(LevelTrace, format, v...)
}

// Panicf is equivalent to l.Printf() followed by a call to panic().
func Panicf(format string, v ...interface{}) {
	logger.Panicf(format, v...)
}

// Panicln is equivalent to l.Println() followed by a call to panic().
func Panicln(v ...interface{}) {
	logger.Panicln(v...)
}
