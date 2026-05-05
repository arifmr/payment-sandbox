package constant

type Role string

const (
	RoleMerchant Role = "MERCHANT"
	RoleAdmin    Role = "ADMIN"
)

func (r Role) Valid() bool {
	switch r {
	case RoleMerchant, RoleAdmin:
		return true
	}
	return false
}
