package keystore

import (
	"fmt"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// CheckFile проверяет структуру keystore-файла без пароля: имя <id>.keystore,
// допустимый id, заголовок и размер (parseFile), id в заголовке равен id в
// имени. Расшифровать файл и проверить пароль функция не может: подмену
// содержимого при неизменном заголовке обнаружит только Verify.
//
// name это имя файла без каталога. Все ошибки имеют код CodeKeystoreCorrupt;
// тексты не содержат содержимого файла.
func CheckFile(name string, data []byte) error {
	bad := func(format string, a ...any) error {
		return domain.NewError(domain.CodeKeystoreCorrupt, fmt.Errorf("keystore: "+format, a...))
	}
	id, ok := strings.CutSuffix(name, fileExt)
	if !ok || !validID(id) {
		return bad("file name is not <id>%s with a valid id", fileExt)
	}
	h, _, _, err := parseFile(data)
	if err != nil {
		return domain.NewError(domain.CodeKeystoreCorrupt, err)
	}
	if !validID(h.id) {
		return bad("wallet id in the header is invalid")
	}
	if h.id != id {
		return bad("wallet id in the header differs from the file name")
	}
	return nil
}
