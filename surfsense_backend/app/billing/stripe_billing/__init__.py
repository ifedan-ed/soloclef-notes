"""Stripe for plans (Billing subscriptions) and credit packs (one-off Checkout).

Test or live is decided by the secret key alone (``sk_test_`` / ``sk_live_``),
set on the billing admin page or in ``STRIPE_SECRET_KEY``. Stripe objects the
code keeps (customer, product and price ids) are re-created on demand when the
configured key cannot see them, so switching keys needs no data migration.
"""
