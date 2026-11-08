"""Write repr(float) for the values enqueued_at and not_after can take.

A unix timestamp with a fraction, the integral second, the boundaries where
repr switches to exponent notation, and a spread of random mantissas: the Go
renderer is compared against every one of them.
"""

import json
import random

values = [
    0.0, -0.0, 1.0, 0.5, 0.0001, 0.00001, 1e-5, 1e15, 1e16, 1e16 - 2.0,
    1000000000000000.0, 1762608000.0, 1762608000.123456, 1762608600.5,
    3.141592653589793, 2.2250738585072014e-308, 1.7976931348623157e308,
    5e-324, 1e100, -1762608000.25, 1e22, 1e23, 123456789012345.67,
]
rng = random.Random(20261108)
for _ in range(2000):
    values.append(rng.uniform(1.7e9, 1.8e9))
    values.append(rng.uniform(0, 1))
    values.append(rng.uniform(-1e18, 1e18) * rng.choice([1e-30, 1.0, 1e30]))

# Each entry is repr(v): the Go renderer parses it and must write it back
# unchanged, which is the property every enqueued_at depends on.
print(json.dumps(sorted({repr(v) for v in values})))
