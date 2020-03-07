package hitbtc

import (
	"log"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

func newID() string {
	uuid, err := uuid.NewRandom()
	if err != nil {
		log.Println(err)
		return ""
	}
	return strings.ReplaceAll(uuid.String(), "-", "")
}

func stf(str string) float64 {
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		//log.Println(err)
		return 0.0
	}
	return f
}

func Quote(str string) string {
	return str[len(str)-3:]
}

func Base(str string) string {
	return str[:len(str)-3]
}

func Truncate(x float64, prec float64) (f float64) {
	f = prec * float64(int(x/prec))
	return
}

func Min(x float64, y float64) float64 {
	if x < y {
		return x
	}
	return y
}

func Max(x float64, y float64) float64 {
	if x > y {
		return x
	}
	return y
}
