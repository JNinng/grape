package biz

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// fakeUserRepo 只记录写入路径关心的字段，其余方法由嵌入接口兜底。
type fakeUserRepo struct {
	UserRepo

	created  *User
	password string
}

func (f *fakeUserRepo) Create(ctx context.Context, u *User) (*User, error) {
	f.created = u
	u.ID = 1
	return u, nil
}

func (f *fakeUserRepo) SetPassword(ctx context.Context, id int64, password string) error {
	f.password = password
	return nil
}

// TestCreateUserHashesPassword 验证创建路径落库的是 bcrypt 哈希，
// 而不是明文。
func TestCreateUserHashesPassword(t *testing.T) {
	repo := &fakeUserRepo{}
	uc := NewUserUsecase(repo)

	u, err := uc.CreateUser(context.Background(), &User{
		Name: "Alice", Email: "alice@example.com", Password: "secret",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Password == "secret" || !strings.HasPrefix(u.Password, "$2") {
		t.Fatalf("password not hashed: %q", u.Password)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte("secret")); err != nil {
		t.Fatalf("hash does not match password: %v", err)
	}
}

// TestSetPasswordHashesPassword 验证改密路径同样哈希后落库。
func TestSetPasswordHashesPassword(t *testing.T) {
	repo := &fakeUserRepo{}
	uc := NewUserUsecase(repo)

	if err := uc.SetPassword(context.Background(), 1, "newpass"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if repo.password == "newpass" {
		t.Fatal("password stored in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(repo.password), []byte("newpass")); err != nil {
		t.Fatalf("hash does not match password: %v", err)
	}
}

// TestCreateUserRejectsTooLongPassword 验证超出 bcrypt 长度上限的密码
// 被拒绝而不是静默截断。
func TestCreateUserRejectsTooLongPassword(t *testing.T) {
	uc := NewUserUsecase(&fakeUserRepo{})

	_, err := uc.CreateUser(context.Background(), &User{
		Name: "Alice", Email: "alice@example.com", Password: strings.Repeat("a", 73),
	})
	if err != ErrUserInvalidArgument {
		t.Fatalf("want ErrUserInvalidArgument, got %v", err)
	}
}
