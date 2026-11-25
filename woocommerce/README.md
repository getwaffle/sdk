# Waffle for WooCommerce (beta)

QRIS and virtual account for WooCommerce through Waffle's hosted page. Setup is in `readme.txt`.
Licensed GPL-2.0-or-later (the rest of this repository is MIT).

- An order is paid only after the charge is read back from the API (webhook, thank-you page, or one scheduled check).
- Amount, currency, mode, site and order key must match. A paid charge with a different amount puts the order on-hold.
- Charges are idempotent per order, mode, amount and attempt.
- IDR only. No refunds or subscriptions.

## Test

Start a local Waffle stack, then one of these (both use port 8899):

- `docker compose up -d`: current WordPress; install the latest WooCommerce.
- `COMPOSE_FILE=docker-compose.min.yml docker compose up -d --build`: PHP 7.4, WordPress 6.4, WooCommerce 8.2.

Set the store currency to IDR with 0 decimals, then run `tests/e2e.py`.

## Release

Bump the version in `waffle-woocommerce.php` (twice) and `readme.txt`, then push a tag `woocommerce/vX.Y.Z`.
