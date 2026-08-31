package core

import "testing"

func TestIsAllowedHubCountry(t *testing.T) {
	if !IsAllowedHubCountry("Nigeria") {
		t.Fatal("expected Nigeria to be an allowed jurisdiction")
	}
	if IsAllowedHubCountry("United States") {
		t.Fatal("expected United States to never be an allowed jurisdiction")
	}
	if IsAllowedHubCountry("") {
		t.Fatal("expected an unset country to be rejected")
	}
	if IsAllowedHubCountry("Not A Real Country") {
		t.Fatal("expected a nonsense country to be rejected")
	}
}

// TestCompanyStore_Review_RefusesApprovalForIneligibleJurisdiction is
// the actual enforcement of "no moderator may approve a US Trading
// Hub" - Review must refuse the approval outright, regardless of what
// the moderator intends, whenever Country isn't on AllowedHubCountries
// (which includes "United States" specifically, since that string
// never appears in the list at all - see that list's own doc comment).
func TestCompanyStore_Review_RefusesApprovalForIneligibleJurisdiction(t *testing.T) {
	s := NewCompanyStore()
	const moderator = "test-moderator"

	tests := []struct {
		name    string
		country string
	}{
		{"United States jurisdiction", "United States"},
		{"unset jurisdiction", ""},
		{"nonsense jurisdiction", "Nowhereland"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := "hub-" + tt.name
			s.Apply(&CompanyApplication{
				Address:          addr,
				Country:          tt.country,
				Status:           CompanyPending,
				ModeratorAddress: moderator,
				CertificateHash:  "real-cert-hash",
			})

			if ok := s.Review(addr, moderator, true, 1000); ok {
				t.Fatalf("expected approval to be refused for country %q", tt.country)
			}

			app, _ := s.Get(addr)
			if app.Status != CompanyPending {
				t.Fatalf("expected the application to remain PENDING, got %s", app.Status)
			}
		})
	}
}

// TestCompanyStore_Review_ApprovesEligibleJurisdiction confirms the
// hard block above is scoped to ineligible jurisdictions only - a
// real, allowed country with a certificate on file still approves
// normally.
func TestCompanyStore_Review_ApprovesEligibleJurisdiction(t *testing.T) {
	s := NewCompanyStore()
	const moderator = "test-moderator"
	const addr = "hub-eligible"

	s.Apply(&CompanyApplication{
		Address:          addr,
		Country:          "Nigeria",
		Status:           CompanyPending,
		ModeratorAddress: moderator,
		CertificateHash:  "real-cert-hash",
	})

	if ok := s.Review(addr, moderator, true, 1000); !ok {
		t.Fatal("expected approval to succeed for an eligible jurisdiction")
	}
	if !s.IsVerified(addr) {
		t.Fatal("expected the hub to be verified")
	}
}

// TestCompanyStore_Review_RejectionAlwaysAllowedRegardlessOfCountry
// confirms the hard block is specific to APPROVAL - a moderator can
// still reject an ineligible-jurisdiction application (there is no
// reason to ever block that).
func TestCompanyStore_Review_RejectionAlwaysAllowedRegardlessOfCountry(t *testing.T) {
	s := NewCompanyStore()
	const moderator = "test-moderator"
	const addr = "hub-us-reject"

	s.Apply(&CompanyApplication{
		Address:          addr,
		Country:          "United States",
		Status:           CompanyPending,
		ModeratorAddress: moderator,
	})

	if ok := s.Review(addr, moderator, false, 1000); !ok {
		t.Fatal("expected rejection to succeed regardless of jurisdiction")
	}
	app, _ := s.Get(addr)
	if app.Status != CompanyRejected {
		t.Fatalf("expected the application to be REJECTED, got %s", app.Status)
	}
}
