"""Create a verified account while registration is closed.

    docker exec -it soloclef-backend-1 python scripts/create_account.py someone@example.com

The password is random and never printed; the person sets theirs with
"Forgot password?" on the sign-in page. With --set-password the command asks
for one instead, so a new server can be used before SMTP is configured:

    docker exec -it soloclef-backend-1 python scripts/create_account.py \
      someone@example.com --set-password

Admin rights are not given here: billing admin comes from
BILLING_ADMIN_EMAILS in docker/.env.
"""

from __future__ import annotations

import argparse
import asyncio
import getpass
import sys

from app.account_provisioning.create import AccountExistsError, create_verified_account


def _asked_password() -> str | None:
    first = getpass.getpass("Password: ")
    second = getpass.getpass("Password again: ")
    if first != second:
        print("The passwords do not match; nothing changed.")
        return None
    return first


async def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("email")
    parser.add_argument(
        "--set-password",
        action="store_true",
        help="ask for a password now instead of leaving a random one to reset",
    )
    args = parser.parse_args()
    password = _asked_password() if args.set_password else None
    if args.set_password and password is None:
        return 2
    try:
        account = await create_verified_account(args.email, password=password)
    except AccountExistsError:
        print(f"An account for {args.email} already exists; nothing changed.")
        return 1
    print(
        f"Created {account.email} (user {account.user_id}), "
        f"workspaces {list(account.workspace_ids)}. "
        + (
            "Password set."
            if password is not None
            else "Password is random and unknown: use Forgot password to set one."
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
