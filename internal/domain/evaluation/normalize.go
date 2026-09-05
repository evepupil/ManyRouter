package evaluation

import (
	"errors"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var (
	ErrUnknownCell   = errors.New("evaluation cell is unsupported")
	ErrInvalidAnswer = errors.New("answer is invalid for the evaluation cell")
)

var englishSmallNumbers = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4,
	"five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
	"ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
	"fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17,
	"eighteen": 18, "nineteen": 19,
}

var englishTens = map[string]int{
	"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
	"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
}

var chineseDigits = map[rune]int{
	'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3,
	'四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
}

var colorAliases = map[string]string{
	"red": "red", "blue": "blue", "green": "green", "yellow": "yellow",
	"black": "black", "white": "white", "purple": "purple", "violet": "purple",
	"orange": "orange", "pink": "pink", "brown": "brown", "gray": "gray",
	"grey": "gray", "cyan": "cyan", "magenta": "magenta", "teal": "teal",
	"indigo": "indigo", "gold": "gold", "golden": "gold", "silver": "silver",
	"beige": "beige", "maroon": "maroon", "navy": "navy", "turquoise": "turquoise",
	"light blue": "light_blue", "dark blue": "dark_blue", "sky blue": "sky_blue",
	"红": "red", "红色": "red", "蓝": "blue", "蓝色": "blue",
	"绿": "green", "绿色": "green", "黄": "yellow", "黄色": "yellow",
	"黑": "black", "黑色": "black", "白": "white", "白色": "white",
	"紫": "purple", "紫色": "purple", "橙": "orange", "橙色": "orange",
	"粉": "pink", "粉色": "pink", "粉红": "pink", "粉红色": "pink",
	"棕": "brown", "棕色": "brown", "褐": "brown", "褐色": "brown",
	"灰": "gray", "灰色": "gray", "青": "cyan", "青色": "cyan",
	"靛蓝": "indigo", "靛蓝色": "indigo", "金": "gold", "金色": "gold",
	"银": "silver", "银色": "silver", "米色": "beige",
}

var coinAliases = map[string]string{
	"head": "heads", "heads": "heads", "h": "heads",
	"tail": "tails", "tails": "tails", "t": "tails",
	"正": "heads", "正面": "heads", "人头": "heads", "花": "heads",
	"反": "tails", "反面": "tails", "字": "tails", "字面": "tails",
}

func NormalizeAnswer(cell CellID, raw string) (string, error) {
	answer := firstAnswerLine(raw)
	if answer == "" {
		return "", ErrInvalidAnswer
	}

	switch cell {
	case CellNumber100EN, CellNumber100ZH:
		return normalizeNumber(answer, 100)
	case CellNumber10EN, CellNumber10ZH:
		return normalizeNumber(answer, 10)
	case CellColorEN, CellColorZH:
		value, ok := colorAliases[normalizedPhrase(answer)]
		if !ok {
			return "", ErrInvalidAnswer
		}
		return value, nil
	case CellCoinEN, CellCoinZH:
		value, ok := coinAliases[normalizedPhrase(answer)]
		if !ok {
			return "", ErrInvalidAnswer
		}
		return value, nil
	default:
		return "", ErrUnknownCell
	}
}

func BuildDistribution(cell CellID, rawAnswers []string) (Distribution, error) {
	if !isKnownCell(cell) {
		return Distribution{}, ErrUnknownCell
	}
	distribution := Distribution{Counts: make(map[string]uint64)}
	for _, raw := range rawAnswers {
		answer, err := NormalizeAnswer(cell, raw)
		if errors.Is(err, ErrInvalidAnswer) {
			distribution.InvalidSamples++
			continue
		}
		if err != nil {
			return Distribution{}, err
		}
		distribution.Counts[answer]++
	}
	return distribution, nil
}

func isKnownCell(cell CellID) bool {
	for _, candidate := range protocolV1Cells {
		if cell == candidate {
			return true
		}
	}
	return false
}

func firstAnswerLine(raw string) string {
	value := norm.NFKC.String(strings.ReplaceAll(raw, "\r\n", "\n"))
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimFunc(line, func(char rune) bool {
			return unicode.IsSpace(char) || unicode.IsPunct(char)
		})
		if line != "" {
			return line
		}
	}
	return ""
}

func normalizedPhrase(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "-", " ")
	return strings.Join(strings.Fields(value), " ")
}

func normalizeNumber(answer string, maximum int) (string, error) {
	candidates := []string{answer}
	if fields := strings.Fields(answer); len(fields) > 1 {
		candidates = append(candidates, strings.TrimFunc(fields[0], unicode.IsPunct))
	}
	for _, candidate := range candidates {
		if number, ok := parseNumber(candidate); ok && number >= 1 && number <= maximum {
			return strconv.Itoa(number), nil
		}
	}
	return "", ErrInvalidAnswer
}

func parseNumber(value string) (int, bool) {
	value = normalizedPhrase(value)
	if value == "" {
		return 0, false
	}
	if digits, ok := decimalDigits(value); ok {
		number, err := strconv.Atoi(digits)
		return number, err == nil
	}
	if number, ok := parseEnglishNumber(value); ok {
		return number, true
	}
	return parseChineseNumber(value)
}

func decimalDigits(value string) (string, bool) {
	var result strings.Builder
	for _, char := range value {
		digit, ok := decimalDigit(char)
		if !ok {
			return "", false
		}
		result.WriteByte(byte('0' + digit))
	}
	return result.String(), result.Len() > 0
}

func decimalDigit(char rune) (int, bool) {
	switch {
	case char >= '0' && char <= '9':
		return int(char - '0'), true
	case char >= '０' && char <= '９':
		return int(char - '０'), true
	case char >= '٠' && char <= '٩':
		return int(char - '٠'), true
	case char >= '۰' && char <= '۹':
		return int(char - '۰'), true
	default:
		return 0, false
	}
}

func parseEnglishNumber(value string) (int, bool) {
	fields := strings.Fields(value)
	withoutAnd := fields[:0]
	for _, field := range fields {
		if field != "and" {
			withoutAnd = append(withoutAnd, field)
		}
	}
	fields = withoutAnd
	if len(fields) == 1 {
		if number, ok := englishSmallNumbers[fields[0]]; ok {
			return number, true
		}
		if number, ok := englishTens[fields[0]]; ok {
			return number, true
		}
		if fields[0] == "hundred" {
			return 100, true
		}
	}
	if len(fields) == 2 {
		if (fields[0] == "one" || fields[0] == "a") && fields[1] == "hundred" {
			return 100, true
		}
		tens, tensOK := englishTens[fields[0]]
		unit, unitOK := englishSmallNumbers[fields[1]]
		if tensOK && unitOK && unit > 0 && unit < 10 {
			return tens + unit, true
		}
	}
	return 0, false
}

func parseChineseNumber(value string) (int, bool) {
	if value == "百" || value == "一百" {
		return 100, true
	}
	if strings.Count(value, "十") == 1 {
		left, right, _ := strings.Cut(value, "十")
		tens := 1
		if left != "" {
			parsed, ok := singleChineseDigit(left)
			if !ok || parsed == 0 {
				return 0, false
			}
			tens = parsed
		}
		units := 0
		if right != "" {
			parsed, ok := singleChineseDigit(right)
			if !ok {
				return 0, false
			}
			units = parsed
		}
		return tens*10 + units, true
	}
	if number, ok := singleChineseDigit(value); ok {
		return number, true
	}
	var digits strings.Builder
	for _, char := range value {
		digit, ok := chineseDigits[char]
		if !ok {
			return 0, false
		}
		digits.WriteByte(byte('0' + digit))
	}
	if digits.Len() == 0 {
		return 0, false
	}
	number, err := strconv.Atoi(digits.String())
	return number, err == nil
}

func singleChineseDigit(value string) (int, bool) {
	runes := []rune(value)
	if len(runes) != 1 {
		return 0, false
	}
	digit, ok := chineseDigits[runes[0]]
	return digit, ok
}
