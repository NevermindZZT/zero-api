package store

// Service 聚合所有 Repository
type Service struct {
	Channel          *ChannelRepo
	Model            *ModelRepo
	Usage            *UsageRepo
	ProxyConfig      *ProxyConfigRepo
	APIKey           *APIKeyRepo
	Skill            *SkillRepo
	SkillCombination *SkillCombinationRepo
	ChannelBalance   *ChannelBalanceRepo

	DB *DB
}

func NewService(db *DB) *Service {
	return &Service{
		Channel:          NewChannelRepo(db),
		Model:            NewModelRepo(db),
		Usage:            NewUsageRepo(db),
		ProxyConfig:      NewProxyConfigRepo(db),
		APIKey:           NewAPIKeyRepo(db),
		Skill:            NewSkillRepo(db),
		SkillCombination: NewSkillCombinationRepo(db),
		ChannelBalance:   NewChannelBalanceRepo(db),
		DB:               db,
	}
}
