package requestbody

import "strconv"

// AtLeast is pydantic's `ge=` constraint, worded as it words it.
func AtLeast(minimum int64) string {
	return "Input should be greater than or equal to " + strconv.FormatInt(minimum, 10)
}
