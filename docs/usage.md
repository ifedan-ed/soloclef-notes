# Usage and limits

Soloclef has no trial, no invite or redeem codes, and nothing to buy. Your
server's usage is whatever your own models and hardware allow.

## Everyone unlimited (recommended)

```dotenv
BILLING_MODE=off
```

Nothing is metered and nobody is ever stopped. Restart the backend, worker and
beat after changing it.

## The administrator unlimited, others with limits

If you share the server and want a ceiling for other people, keep the meter
running:

| `BILLING_MODE` | What it does |
| --- | --- |
| `off` | Meters nothing. Everyone is unlimited. |
| `shadow` (the default) | Records what each action would use, but never blocks anything. |
| `live` | Each person has a monthly allowance; work stops when it runs out, until the next month. |

Whatever the mode, **the accounts in `BILLING_ADMIN_EMAILS` are always
unlimited.**

The administrator manages everyone else under **User settings → Billing →
Admin**:

- Users → Exempt (unlimited): gives one person unlimited use, with a
  reason you write for yourself.
- Users → Adjust credits: adds to one person's allowance (a negative
  number takes some away).
- Plans: the monthly allowance everyone gets.
- Mode & payments: the mode above. A choice saved here wins over
  `docker/.env`.

## Payments

Payments are off unless you set them up yourself (Stripe or your own BTCPay
Server; see the in-app guide at `/docs/self-hosting`). With none set, no
price or checkout is ever shown.
