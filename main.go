package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pkg/errors"
	"gopkg.in/alecthomas/kingpin.v2"
)

type txtProvider interface {
	getTxtRecords() ([]string, error)
}

func getTxtProvider(options *options, source string) (txtProvider, error) {
	if strings.ContainsRune(source, ':') {
		uri, err := parseURI(source)
		if err != nil {
			return nil, err
		}
		switch uri.scheme {
		case "dns":
			domain := uri.path
			if uri.query != "" {
				return nil, fmt.Errorf("unexpected \"%s\": queries in DNS URIs not supported", uri.query)
			}
			if uri.fragment != "" {
				return nil, fmt.Errorf("unexpected \"%s\": fragments in DNS URIs not supported", uri.fragment)
			}
			if strings.HasPrefix(domain, "/") {
				domain = domain[1:len(domain)]
			}
			return makeDnsProvider(options, uri.authority, domain)
		}
	}
	return makeDnsProvider(options, "", source)
}

type options struct {
	outputFormat string
	valueType    string
	nameserver   string
}

func makeDefaultOptions() *options {
	return &options{
		outputFormat: "plain",
		valueType:    "single",
	}
}

func output(options *options, sink io.Writer, values []string) error {
	if options.valueType == "single" && len(values) != 1 {
		return fmt.Errorf("expected 1 value but got %d (%v)", len(values), values)
	}
	switch options.outputFormat {
	case "json":
		var err error
		encoder := json.NewEncoder(sink)
		encoder.SetEscapeHTML(false)
		switch options.valueType {
		case "single":
			err = encoder.Encode(values[0])
		case "list":
			err = encoder.Encode(values)
		}
		if err != nil {
			return errors.Wrap(err, "error writing JSON")
		}
	case "plain":
		for _, record := range values {
			if _, err := fmt.Fprintln(sink, record); err != nil {
				return err
			}
		}
	}
	return nil
}

func lookUpValues(options *options, txtRecords []string, key string, defaultValues []string) ([]string, error) {
	var values []string
	for _, record := range txtRecords {
		pieces := strings.SplitN(record, "=", 2)
		if len(pieces) > 1 && pieces[0] == key {
			values = append(values, pieces[1])
		}
	}
	if len(values) == 0 {
		values = defaultValues
	}

	if options.valueType == "single" {
		if len(values) == 0 {
			return nil, errors.Errorf("no values found for key %s, and no default provided", key)
		}
		if len(values) > 1 {
			return nil, errors.Errorf("%d values found for key %s, but only 1 was expected", len(values), key)
		}
	}

	return values, nil
}

func main() {
	options := makeDefaultOptions()
	kingpin.Version("0.4.0")
	kingpin.CommandLine.HelpFlag.Short('h')
	kingpin.Flag("format", "Output format (json, plain, zero)").Short('f').Default("plain").Envar("SDGET_FORMAT").EnumVar(&options.outputFormat, "json", "plain", "zero")
	kingpin.Flag("nameserver", "Default nameserver address (ns.example.com:53, 127.0.0.1)").Short('@').Envar("SDGET_NAMESERVER").StringVar(&options.nameserver)
	kingpin.Flag("type", "Data value type (single, list)").Short('t').Default("single").Envar("SDGET_TYPE").EnumVar(&options.valueType, "single", "list")
	source := kingpin.Arg("source", "URI or domain name to query for TXT records").Required().String()
	key := kingpin.Arg("key", "Key name to look up in source").Required().String()
	defaultValues := kingpin.Arg("default", "Default value(s) to use if key is not found").Strings()
	kingpin.Parse()

	if *defaultValues == nil {
		defaultValues = &[]string{}
	}

	if options.valueType == "single" && len(*defaultValues) > 1 {
		fmt.Fprintf(os.Stderr, "Got %n default values, but the value type is \"single\".  (Did you mean to set --type list?)\n", len(*defaultValues))
		os.Exit(1)
	}

	provider, err := getTxtProvider(options, *source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up client: %s\n", err.Error())
		os.Exit(2)
	}

	txtRecords, err := provider.getTxtRecords()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error looking up TXT records:\n%+v\n", err.Error())
		os.Exit(3)
	}

	var values []string
	values, err = lookUpValues(options, txtRecords, *key, *defaultValues)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error looking up values for key \"%s\" in %s:\n%+v\n", *key, *source, err.Error())
		os.Exit(4)
	}

	if err = output(options, os.Stdout, values); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output values: %s\n", err.Error())
		os.Exit(5)
	}
}
