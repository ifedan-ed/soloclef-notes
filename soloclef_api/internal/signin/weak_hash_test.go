package signin

import "golang.org/x/crypto/argon2"

func deriveForTest(password string, salt []byte, time, memory uint32, threads uint8) []byte {
	return argon2.IDKey([]byte(password), salt, time, memory, threads, argonKeyLen)
}
