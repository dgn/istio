// Code generated by protoc-gen-validate. DO NOT EDIT.
// source: envoy/config/filter/network/mysql_proxy/v1alpha1/mysql_proxy.proto

package envoy_config_filter_network_mysql_proxy_v1alpha1

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang/protobuf/ptypes"
)

// ensure the imports are used
var (
	_ = bytes.MinRead
	_ = errors.New("")
	_ = fmt.Print
	_ = utf8.UTFMax
	_ = (*regexp.Regexp)(nil)
	_ = (*strings.Reader)(nil)
	_ = net.IPv4len
	_ = time.Duration(0)
	_ = (*url.URL)(nil)
	_ = (*mail.Address)(nil)
	_ = ptypes.DynamicAny{}
)

// define the regex for a UUID once up-front
var _mysql_proxy_uuidPattern = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

// Validate checks the field values on MySQLProxy with the rules defined in the
// proto definition for this message. If any rules are violated, an error is returned.
func (m *MySQLProxy) Validate() error {
	if m == nil {
		return nil
	}

	if len(m.GetStatPrefix()) < 1 {
		return MySQLProxyValidationError{
			field:  "StatPrefix",
			reason: "value length must be at least 1 bytes",
		}
	}

	// no validation rules for AccessLog

	return nil
}

// MySQLProxyValidationError is the validation error returned by
// MySQLProxy.Validate if the designated constraints aren't met.
type MySQLProxyValidationError struct {
	field  string
	reason string
	cause  error
	key    bool
}

// Field function returns field value.
func (e MySQLProxyValidationError) Field() string { return e.field }

// Reason function returns reason value.
func (e MySQLProxyValidationError) Reason() string { return e.reason }

// Cause function returns cause value.
func (e MySQLProxyValidationError) Cause() error { return e.cause }

// Key function returns key value.
func (e MySQLProxyValidationError) Key() bool { return e.key }

// ErrorName returns error name.
func (e MySQLProxyValidationError) ErrorName() string { return "MySQLProxyValidationError" }

// Error satisfies the builtin error interface
func (e MySQLProxyValidationError) Error() string {
	cause := ""
	if e.cause != nil {
		cause = fmt.Sprintf(" | caused by: %v", e.cause)
	}

	key := ""
	if e.key {
		key = "key for "
	}

	return fmt.Sprintf(
		"invalid %sMySQLProxy.%s: %s%s",
		key,
		e.field,
		e.reason,
		cause)
}

var _ error = MySQLProxyValidationError{}

var _ interface {
	Field() string
	Reason() string
	Key() bool
	Cause() error
	ErrorName() string
} = MySQLProxyValidationError{}
