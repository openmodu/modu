# modu-web progress

This file tracks small user-facing website changes for `modu-web`.

## Done

- 2026-06-30: added footer entry links for 服务条款 and 隐私协议, plus matching
  static legal pages with shared Modu navigation, footer, and legal document
  styles. Configured Vite multi-page build output so the legal pages are
  included in deploy artifacts.
- 2026-07-01: made the footer legal links more discoverable with a dedicated
  Legal group and bordered link buttons across the home and legal pages.
- 2026-07-01: expanded legal coverage for review feedback: added free-product
  refund language, violation consequences, third-party privacy sharing details,
  user data rights, a pricing page, and a direct GitHub Issues support entry.
- 2026-07-01: replaced the GitHub Issues support entry with the customer
  support email `dev@crosszan.com` across the footer, legal pages, pricing
  page, and privacy data-rights/contact language.
- 2026-07-01: made the customer support email visible directly in the footer
  and wrapped mail links with Cloudflare email-obfuscation opt-out comments.
- 2026-07-01: rendered the support email as split text spans instead of
  `mailto:` links so Cloudflare does not rewrite it to email-protection URLs.
