package buildstamp

import "strings"

var rawStampData string
var stampData map[string]string

func init() {
	if rawStampData != "" {
		stampData = make(map[string]string)
		for _, line := range strings.Split(rawStampData, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line[0] == '#' {
				continue
			}
			var key, value string
			if space := strings.IndexAny(line, " \t"); space == -1 {
				key = line
			} else {
				key = line[:space]
				value = strings.TrimSpace(line[space+1:])
			}
			stampData[key] = value
		}
	}
}

func Stamped() bool {
	return stampData != nil
}

func Raw() string {
	return rawStampData
}

func Keys() []string {
	if stampData == nil {
		return nil
	}
	keys := make([]string, 0, len(stampData))
	for k, _ := range stampData {
		keys = append(keys, k)
	}
	return keys
}

func Value(key string) (value string, ok bool) {
	value, ok = stampData[key]
	return
}
