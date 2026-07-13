package core

// KYCRecord is a self-attested KYC submission - explicitly NOT real
// identity-document verification (no ID upload, no third-party
// verification provider integrated), consistent with this codebase's
// other disclosed simplifications (see core/company_store.go's
// free-text DocumentNote). Real document verification is a distinct,
// larger undertaking appropriate for closer to a real audited launch.
type KYCRecord struct {
	Address     string `json:"address"`
	FullName    string `json:"fullName"`
	DateOfBirth string `json:"dateOfBirth"` // "YYYY-MM-DD", self-reported
	Country     string `json:"country"`
	AgreedTerms bool   `json:"agreedTerms"`
	SubmittedAt int64  `json:"submittedAt"`
}

// MinReputationForLending is the "security pass" half of the lending
// gate - the Silver credit-tier threshold (see core/credit_score.go).
// Lowered from the original Gold threshold (60) so a wallet at or
// near the default starting reputation of 50 already qualifies,
// rather than needing to build up a real track record first - the
// gate's remaining job is filtering out only wallets that have
// actually fallen below Silver (e.g. via slashing or defaults), not
// screening out brand-new participants. Combined with AgreedTerms,
// this is still more than cosmetic - a real, currently-held standing
// on the network, not just a form someone filled in once.
const MinReputationForLending = 40

// KYCStore tracks one self-attested record per address.
type KYCStore struct {
	Records map[string]KYCRecord `json:"records"`
}

func NewKYCStore() *KYCStore {
	return &KYCStore{Records: make(map[string]KYCRecord)}
}

// Submit records/replaces address's self-attested KYC fields.
func (s *KYCStore) Submit(address, fullName, dob, country string, agreedTerms bool, submittedAt int64) KYCRecord {
	rec := KYCRecord{
		Address:     address,
		FullName:    fullName,
		DateOfBirth: dob,
		Country:     country,
		AgreedTerms: agreedTerms,
		SubmittedAt: submittedAt,
	}
	s.Records[address] = rec
	return rec
}

func (s *KYCStore) Get(address string) (KYCRecord, bool) {
	rec, ok := s.Records[address]
	return rec, ok
}

// IsApproved re-derives lending access from the address's CURRENT
// reputation every time it's called, rather than caching an
// "approved" bit at submission time - reputation can rise or fall
// after submission (e.g. via slashing or reward accrual elsewhere),
// and access should always track present standing, never a stale
// snapshot. See MinReputationForLending's doc comment for the
// threshold's reasoning.
func (s *KYCStore) IsApproved(address string, currentReputation uint64) bool {
	rec, ok := s.Records[address]
	if !ok || !rec.AgreedTerms {
		return false
	}
	return currentReputation >= MinReputationForLending
}
