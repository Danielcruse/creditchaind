# KYC/AML Vendor Research — Real Identity Verification for CreditChain

**Status:** Research only. No code changed. Nothing in this document has been wired into `core/company.go` or anywhere else.
**Date:** 2026-08-19
**Author:** Research pass requested by the CreditChain owner, prepared by Claude.
**Purpose:** Compare commercial KYC/KYB/AML vendors as candidates to eventually replace the disclosed testnet-only placeholder in `core/company.go` (`CompanyApplication` — currently a moderator eyeballing free text plus one uploaded document, explicitly not real KYC/AML).

**This document does not select a vendor.** It narrows the field and lays out real comparative information so the owner can make that call, including any pricing/contract negotiation. See the Recommendation section at the end for the two vendors suggested as a starting point for evaluation, and why.

---

## 1. Why this matters for CreditChain specifically

CreditChain's badge system needs to eventually cover **two distinct verification types**, not one:

1. **Individual KYC** — for traders, if/when individual verification is added. Government ID + liveness/selfie match is the baseline; proof-of-address and sanctions/PEP screening are usually bolted on for anything touching real money movement.
2. **Business KYB (Know Your Business)** — for Trading Hubs. Hubs are legal entities, so this needs company registry lookups, ultimate beneficial owner (UBO) identification, and verification of the humans authorized to act for the entity — which in practice means **KYB always sits on top of KYC**, not instead of it (someone still has to prove they're a real, authorized person before their business claims are trusted).

Because CreditChain bridges real ETH/BTC/USDC through a smart-contract vault, a "Verified Trading Company" badge backed by real KYC/KYB is a compliance control, not just a trust badge — sanctions screening in particular has real legal exposure if skipped. That's the lens used below: not "which vendor has the nicest dashboard" but "which vendor lets a small Go team stand up a defensible individual+business verification flow without a large integration or compliance-ops lift."

## 2. Vendors covered

Individually profiled: **Persona, Sumsub, Jumio, Onfido (Entrust), Veriff, ComplyAdvantage**.
Also covered as relevant additional players: **Trulioo** and **Alloy** (both came up repeatedly in crypto-KYB research and are worth knowing about even if not deep-profiled), plus a short note on **crypto-native transaction/wallet screening tools** (Chainalysis, TRM Labs, Notabene) which solve a *different* problem than onboarding KYC and are called out only so they aren't confused with it.

---

## 3. Vendor profiles

### 3.1 Persona

**What it verifies:** Government ID document verification, selfie/liveness biometric match, database verification (SSN/phone/email cross-checks), and — as of Persona's 2024 "Know Your Business" expansion — integrated KYB (business registry checks, UBO identification) explicitly designed to chain into KYC so the business's authorized individuals get verified through the same flow. PEP/sanctions screening is available as an add-on.

**Pricing:** Publicly semi-transparent relative to peers. Reported figures: roughly **$1.50 per KYC verification**, with an "Essential" plan starting around **$250/month** flat-rate for core products. KYB and AML add-ons are priced separately and pricing at meaningful volume moves to custom/sales-quoted. Treat exact numbers as directional — Persona, like all these vendors, ultimately quotes final pricing through sales once volume and feature mix are known.

**API/integration:** REST API with well-regarded developer documentation (`docs.withpersona.com`), webhooks for async verification results, and official SDKs for major web/mobile stacks. No official Go SDK, but the API is plain REST/JSON — calling it from a Go backend with `net/http` or a generated client is straightforward; this is true of every vendor in this report, since none officially support Go.

**Crypto-industry focus:** Actively marketed to crypto exchanges (Persona publishes case studies, e.g. an unnamed "international crypto exchange" and reports a crypto customer doubling conversion after switching). Not a crypto-only specialist the way Sumsub is, but has real traction there. Customers across tech broadly include Ripple, Square, Reddit, Coinbase-adjacent fintechs (per public case studies).

**Jurisdiction notes:** KYB automation explicitly covers the EU/EEA, UK, and Canada well today, with APAC/LATAM business verification in early access — meaning KYB coverage outside NA/EU/UK may be less mature right now than Persona's individual KYC coverage.

**Notable risk flag:** In February 2026, security researchers reported that Persona had left a large internal dashboard codebase (thousands of files, tens of megabytes) exposed on an unprotected, government-authorized endpoint while it was Discord's identity-verification vendor for a UK age-verification pilot. Discord publicly cut ties with Persona and delayed its rollout as a result. This isn't necessarily disqualifying — every vendor here handles extremely sensitive documents and any of them could have an incident — but it's a concrete, recent, named data-handling failure specific to Persona and worth factoring into vendor due diligence (ask about it directly, ask for their SOC 2 report and incident history).

---

### 3.2 Sumsub

**What it verifies:** Document verification, liveness/selfie biometrics, proof-of-address, and a genuinely mature KYB module (company registry checks, UBO identification, corporate document verification, business license validation) sold as part of one unified platform rather than a bolted-on acquisition. Also bundles AML/sanctions screening, ongoing transaction monitoring, and travel-rule tooling for crypto specifically.

**Pricing:** The most transparently published pricing of any vendor in this list. Publicly listed tiers: **Basic ≈ $1.35/verification with a $149/month minimum**; **Compliance ≈ $1.85/verification with a $299/month minimum** (adds AML screening + ongoing monitoring); Enterprise is custom. Volume discounting reportedly brings per-check cost down toward $0.50–$3 depending on region and volume at scale.

**API/integration:** REST API (`api.sumsub.com`) documented at `docs.sumsub.com`, with production/sandbox environments and HMAC-SHA256 app-token authentication. No official Go SDK, but there are two community-maintained ones (`github.com/thrownew/go-sumsub`, `github.com/cooli88/sumsubgo`) — a signal that enough Go shops use Sumsub's REST API that people bothered to wrap it, which is a reasonable proxy for "this REST API is easy enough to call from Go that it's been done before."

**Crypto-industry focus:** This is Sumsub's core market positioning — it markets a dedicated "crypto" product line, publishes crypto-specific case studies and pricing pages, and is one of the most commonly cited KYC vendors specifically among crypto exchanges and Web3 platforms in third-party comparisons. It also recently announced a partnership with ComplyAdvantage to strengthen AML screening further, suggesting Sumsub's own sanctions-list coverage is complemented rather than fully self-sufficient at the deepest tier.

**Jurisdiction notes:** Broad global document coverage; regularly cited in crypto-exchange vendor comparisons as strong on emerging-market ID types, which matters if CreditChain's trader base is geographically broad rather than US/EU-concentrated.

---

### 3.3 Jumio

**What it verifies:** Document verification (5,000+ ID types across 200 countries/territories, per Jumio's own figures — the largest claimed document library of this group), biometric/liveness matching, and AML/watchlist screening. Jumio markets itself as a "KYX" (Know Your Everything) platform. KYB exists but multiple third-party 2026 comparisons characterize Jumio's core strength as still individual identity verification and biometrics, with KYB positioned as a secondary capability rather than the differentiator (unlike Sumsub or Trulioo, where KYB is a headline feature).

**Pricing:** The **least transparent** of the group. Jumio does not publish pricing and quotes volume-based enterprise contracts through sales. Third-party estimates for low-volume usage (<10K verifications/year) suggest **$3–$8 per verification** at list-ish rates, though other estimates put effective per-verification cost closer to $0.90–$2.30 at scale — a wide enough spread that it's not really pinnable down without a sales conversation. Contract minimums are commonly reported in the **$25,000–$100,000+/year** range, which is a meaningfully higher floor than Persona, Sumsub, or Veriff for a platform that is currently small-scale.

**API/integration:** REST API with both cloud and on-premise deployment options — on-prem is unusual among this group and could matter if CreditChain ever needs to keep verification data off a third-party cloud for regulatory reasons, but it's also more integration overhead than a small team likely wants right now.

**Crypto-industry focus:** Jumio publishes a dedicated crypto industry page and explicitly targets crypto exchanges with its AML/biometric combination. It's a long-established, enterprise-grade vendor (processed 1B+ verifications historically) more associated with large exchanges and banks than early-stage platforms.

**Jurisdiction notes:** Broadest claimed document-type coverage of the group (5,000+ ID types), which is a real strength for a platform with a geographically diverse or unpredictable user base.

**Assessment for CreditChain's stage:** Jumio's pricing floor and enterprise-sales-only model make it a weaker fit for a currently-small platform versus Persona/Sumsub/Veriff, even though its underlying tech is well-regarded.

---

### 3.4 Onfido (now part of Entrust)

**What it verifies:** Document verification (2,500+ document types, 195 countries), selfie biometrics (Onfido claims 99.9% fraud-detection accuracy on this), and watchlist/AML screening (sanctions, PEP, adverse media) with optional ongoing monitoring. KYB/business verification exists but is less prominently marketed than Sumsub's, Persona's, or Trulioo's — the public search results returned relatively little Onfido-specific KYB detail compared to those three, suggesting it's a secondary offering rather than a core strength.

**Pricing:** Not published; volume-based, sales-quoted. Third-party estimates suggest contracts starting around **$50,000/year**, though this varies significantly by volume and feature set — similar order of magnitude to Jumio's higher end, i.e. a heavier commitment than Persona/Sumsub/Veriff's published entry tiers.

**API/integration:** REST-based API (create applicants, launch verification workflows, generate SDK tokens, manage checks) with mature SDKs across major platforms. Well documented; no official Go SDK, same as the rest of the group.

**Important context:** Onfido was **acquired by Entrust in April 2024** and continues operating under its own brand inside Entrust's broader identity-security portfolio. This is worth knowing going in — it means Onfido's product roadmap and support structure are now governed by a larger, more enterprise/government-security-focused parent company (Entrust's core business is PKI, certificates, and government ID issuance), which can mean more stability but also less crypto-native focus and potentially slower, more enterprise-paced sales/support cycles for a small platform.

**Crypto-industry focus:** Used across crypto and fintech platforms per general search results, but Onfido does not market itself as crypto-specialized the way Sumsub or Jumio do — it reads as a general-purpose enterprise identity vendor that crypto companies use, not a crypto-first vendor.

**Assessment for CreditChain's stage:** Similar concern to Jumio — high pricing floor, sales-only pricing, and now folded into a larger acquirer — makes it a less natural first evaluation candidate for a small team right now, though worth keeping on a longer list if CreditChain scales toward enterprise/regulated-bank-grade requirements later.

---

### 3.5 Veriff

**What it verifies:** Document/ID scanning with OCR, biometric liveness and face-match checks, and AML/KYC compliance including watchlist and PEP screening. **KYB is new**: Veriff acquired the Estonian KYB provider **Vespia in February 2026** specifically to move from pure individual identity verification into full business verification, with Vespia's tech (300+ jurisdiction business registry data, UBO/shareholder verification for complex ownership structures, configurable onboarding workflows, ongoing monitoring) rolling out via a Limited Access Program in early 2026 and targeted for general commercial availability by mid-2026.

**Pricing:** Among the most transparent and lowest published entry pricing of the group: reported figures show **Essential plan starting at $0.80/verification with a $49/month minimum**, up to **Premium at $1.89/verification with a $209/month minimum**. Broader market estimates cite $2–$6/session depending on features/volume/contract term — so, similar to the others, the low advertised number is a floor, not necessarily what a real deployment with AML add-ons will cost, but the published starting point is meaningfully lower than Jumio or Onfido's.

**API/integration:** REST API with AI/ML-based liveness and an automated decision engine; broad language/document support (48+ languages, 230+ countries' government IDs — the broadest claimed *country* coverage of the group, versus Jumio's broadest claimed *document-type* count). No official Go SDK; same REST-integration profile as the rest.

**Crypto-industry focus:** Veriff explicitly names crypto as one of its focus verticals alongside marketplaces, gaming, and mobility — real but not as singularly crypto-focused as Sumsub's positioning.

**Important timing caveat for CreditChain:** Because Veriff's KYB offering is brand new (acquired Feb 2026, still rolling out toward mid-2026 general availability as of this research), it is **less proven in production for the business-verification half of CreditChain's requirement** than Sumsub's or Persona's KYB, which have been live longer. Veriff's individual KYC is mature and well-established; its KYB is not yet.

---

### 3.6 ComplyAdvantage — AML/sanctions screening specialist

ComplyAdvantage is a **different category of tool** than the five above: it does not do document verification, liveness, or biometric ID checks at all. It is a pure **AML/financial-crime risk platform** — sanctions list screening, PEP screening, adverse media monitoring, and transaction monitoring, backed by a proprietary maintained database rather than document-verification tech.

**What it verifies:** Name/entity screening against sanctions, PEP, and adverse-media databases; ongoing/continuous monitoring after onboarding; transaction-level risk scoring. It explicitly supports segmenting rules by currency, jurisdiction, and volume, and can incorporate crypto-specific data like wallet addresses into its rules.

**Pricing:** Starts at **$99/month on a "Starter" usage-based plan**, and ComplyAdvantage offers a **free "ComplyLaunch" tier for early-stage startups** — notably the most accessible entry point of any vendor in this report for a small platform, and worth flagging given CreditChain's current scale.

**API/integration:** REST-based "Mesh API" for case management, screening, and monitoring automation — same integration profile as the rest (no official Go SDK, but plain REST/JSON).

**Crypto-industry focus:** Explicitly markets to crypto, ICO, and other high-risk-sector customers, and its rules engine has first-class support for crypto-specific fields (wallet addresses, currency type).

**How this fits CreditChain:** ComplyAdvantage is worth treating as **complementary, not competing**, with the document-verification vendors above. In practice, several of the vendors above (Sumsub, Persona, Jumio, Onfido, Veriff) already bundle basic sanctions/PEP screening as an add-on to their core KYC/KYB product — the real decision is whether that bundled screening is good enough, or whether CreditChain wants a dedicated best-of-breed AML engine sitting behind whichever document-verification vendor is chosen (this is exactly what Sumsub's own recent ComplyAdvantage partnership suggests even Sumsub thinks is sometimes warranted). For a small platform, starting with a bundled AML add-on from the primary KYC/KYB vendor and revisiting ComplyAdvantage later if sanctions-screening depth becomes a gap is the lower-complexity path.

---

## 4. Other notable players (mentioned, not deep-profiled)

**Trulioo** — A KYC/KYB specialist claiming coverage of 700M+ business entities and real-time (15-second-refresh) data, accessed through a single-token/single-endpoint API design that's explicitly built for simplicity across many regional data sources. Reports strong recent crypto-exchange traction (one named case: a US crypto exchange raised verification pass-rates from 11% to 60% after adopting Trulioo). Pricing is opaque (~$99/month reported entry point, real cost is volume-negotiated). Worth a look specifically because of its API-simplicity pitch and strong KYB entity-coverage claims, though it's less frequently the default recommendation in crypto-specific comparisons versus Sumsub/Persona.

**Alloy** — Positions itself as a risk/decisioning orchestration layer (not just a raw verification API) that can plug into 250+ underlying data sources including several of the vendors above, plus "perpetual KYC" (continuous re-verification/monitoring rather than one-time checks). Markets directly to crypto/stablecoin platforms. No public pricing found. This is a heavier, more "build your own compliance stack" tool more suited to a company that already knows exactly what it wants than a first integration for a small team — flagged for awareness rather than as a near-term candidate.

**Crypto-native transaction/wallet screening (Chainalysis, TRM Labs, Notabene, etc.)** — These solve a genuinely different problem: screening on-chain wallet addresses and transactions for sanctions/illicit-activity exposure (and, for Notabene specifically, Travel Rule compliance between VASPs), not verifying who a human or business *is* during onboarding. Not a substitute for the onboarding-KYC/KYB vendors above. Flagged here only so they aren't confused with the identity-verification category this report focuses on — if/when CreditChain's on-chain vault activity grows, on-chain screening is a separate, later research question worth its own pass.

---

## 5. Comparison table

| Vendor | Individual KYC | Business KYB | AML/Sanctions | Published entry pricing | API for Go backend | Crypto focus | Notable caveat |
|---|---|---|---|---|---|---|---|
| **Persona** | Yes, mature | Yes, mature (EU/UK/CA strong; APAC/LATAM early access) | Add-on | ~$1.50/verification; ~$250/mo Essential | REST, well-documented, no official Go SDK | Real, not primary focus | Feb 2026 data-exposure incident (Discord) |
| **Sumsub** | Yes, mature | Yes, mature — genuine core strength | Bundled + ComplyAdvantage partnership | $1.35–$1.85/verification, $149–$299/mo minimum (published) | REST, community Go SDKs exist | Core market — crypto-first positioning | — |
| **Jumio** | Yes, mature, broadest doc-type coverage | Secondary capability | Bundled | Not published; est. $3–$8/verification, $25K–$100K+/yr contracts | REST, cloud or on-prem | Real, enterprise-oriented | Highest cost floor of the group |
| **Onfido (Entrust)** | Yes, mature | Secondary, less prominent | Bundled | Not published; est. $50K+/yr | REST, mature SDKs | General-purpose, not crypto-specialized | Now inside Entrust (2024 acquisition); enterprise pace |
| **Veriff** | Yes, mature, broadest country coverage | New (Vespia acquisition Feb 2026, GA ~mid-2026) | Bundled | $0.80–$1.89/verification, $49–$209/mo minimum (published) | REST, well-documented | Named focus vertical | KYB immature/unproven in production |
| **ComplyAdvantage** | No (not a KYC vendor) | No (not a KYB vendor) | Yes — this is its core product | $99/mo Starter; free startup tier | REST ("Mesh API") | Explicit crypto/high-risk-sector focus | Complementary tool, not a full KYC/KYB solution |
| **Trulioo** (other player) | Yes | Yes — strong entity-coverage claims | Bundled | Not published; ~$99/mo reported entry | REST, single-endpoint design | Growing crypto traction | Less commonly the default crypto pick |
| **Alloy** (other player) | Via orchestrated sources | Yes | Yes, incl. continuous/pKYC | Not published | REST | Markets to crypto/stablecoin platforms | Orchestration layer, heavier lift |

All pricing figures are from public/third-party sources as of this research and should be treated as directional. Every vendor in this table ultimately quotes final pricing through a sales conversation once volume, jurisdictions, and feature mix are known — the owner should get real quotes before deciding, not rely on the numbers above.

---

## 6. Recommendation

This is a recommendation of **which vendors to evaluate first**, not a vendor selection — that decision, and any resulting contract or pricing negotiation, is the owner's to make.

**Suggested first evaluation: Sumsub, then Persona as the second option.**

Reasoning, weighed against CreditChain's actual constraints (needs both individual KYC and business KYB together, currently small-scale, small team, wants low integration complexity):

- **Sumsub** is the strongest match on the single requirement that matters most here — it's the only vendor of the six where **KYB is a mature, core, long-standing part of the product** rather than a recent acquisition (Veriff), a secondary feature (Jumio, Onfido), or a still-maturing add-on. It also has the most transparent published pricing of the group, the lowest realistic entry commitment ($149–$299/month minimums, not $25K–$100K/year contracts), the deepest crypto-industry positioning and case-study base, and — notably — community-built Go client libraries already exist, which is a real signal that other small Go teams have integrated it without friction. The main watch-item is that Sumsub itself apparently felt the need to partner with ComplyAdvantage to strengthen sanctions screening, so if AML depth becomes a sticking point later, that's a known, already-available upgrade path rather than a dead end.

- **Persona** is the strongest second option: equally mature KYC+KYB, comparably accessible pricing and entry point, strong developer-experience reputation, and real crypto-exchange traction. It's worth evaluating in parallel with Sumsub specifically to compare integration experience and quote pricing side-by-side before committing. The one thing the owner should factor in and ask Persona directly about during evaluation is the February 2026 data-exposure incident involving Discord — not necessarily disqualifying, but worth a direct conversation about their security posture, SOC 2 status, and what changed since.

**Why not the others as a first pick right now:**
- **Jumio and Onfido** are well-regarded, enterprise-grade, and worth keeping on a longer list if CreditChain scales toward bank-grade regulatory requirements later — but their sales-only pricing with high contract floors ($25K–$100K+/yr for Jumio, ~$50K+/yr for Onfido) is a mismatch for a currently-small platform, and both have KYB as a secondary rather than headline capability.
- **Veriff** is attractive on individual KYC pricing and country coverage, but its KYB offering is brand new (weeks old as of this research, not yet at general availability) — a real risk for a company that needs *both* halves of the requirement working well from day one. Worth revisiting once Veriff's KYB reaches general availability and has some production track record, but not a first pick today.
- **ComplyAdvantage** shouldn't be evaluated as a KYC/KYB replacement at all — it's a complementary AML layer. The practical recommendation is to start with whichever primary vendor is chosen (Sumsub/Persona)'s bundled AML/sanctions screening, and only bring in ComplyAdvantage standalone later if that bundled screening proves insufficient.
- **Trulioo and Alloy** are reasonable to keep in peripheral view — Trulioo in particular has a genuinely strong KYB/entity-coverage story — but neither surfaced as more compelling than Sumsub or Persona for a first, low-complexity integration by a small team.

**Practical next step:** Request live quotes and sandbox API access from both Sumsub and Persona, run a small integration spike against each sandbox from the Go backend (both are plain REST/JSON, so this is a small, low-risk time investment), and compare actual developer experience and final quoted pricing before the owner picks one. Final selection and contract terms remain the owner's call.
