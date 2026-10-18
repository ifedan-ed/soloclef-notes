"""Crypto payments behind one provider interface.

``provider.py`` is the interface; ``btcpay.py`` the one implementation
(the owner's BTCPay Server: Bitcoin, USDT on Tron and Polygon). ``settlement.py`` applies a
provider's status to a ``billing_payments`` row the same way whatever the
provider: packs grant credits, prepaid months extend a plan. Crypto never
renews by itself; there are no crypto subscriptions.
"""
