package entities

type User struct {
	Phone string `gorm:"primaryKey"`
	Name  string `gorm:"index"`
}

type Tokens struct {
	UserPhone string `json:"-" gorm:"primaryKey"`
	User      User   `json:"-" gorm:"constraint:OnDelete:CASCADE"`

	RefreshToken          string      `json:"refreshToken"`
	RefreshTokenExpiresIn *DateTimeTZ `json:"refreshTokenExpiresIn,omitempty"`
	Token                 string      `json:"token"`
	TokenExpireIn         DateTimeTZ  `json:"tokenExpireIn"`
}
