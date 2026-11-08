package workspaceroles

import "testing"

// A partial update turns on whether the request *mentioned* a field, which is
// what model_dump(exclude_unset=True) reports. The three cases below are three
// different writes and the Python tier answers each differently: an absent name
// leaves the column alone, an explicit null writes NULL into a NOT NULL column
// and is a 500, and a name writes the name.
func TestReadChangesTellsAbsentFromNull(t *testing.T) {
	changes, failures, err := ReadChanges("application/json", []byte(`{}`))
	if err != nil || len(failures) > 0 {
		t.Fatalf("an empty body is valid: %v %v", failures, err)
	}
	if changes.Name.Present || changes.Description.Present ||
		changes.Permissions.Present || changes.IsDefault.Present {
		t.Errorf("an empty body mentions nothing: %+v", changes)
	}

	changes, failures, err = ReadChanges("application/json", []byte(`{"name":null,"permissions":null,"is_default":null,"description":null}`))
	if err != nil || len(failures) > 0 {
		t.Fatalf("explicit nulls are valid for RoleUpdate: %v %v", failures, err)
	}
	for name, field := range map[string]bool{
		"name": changes.Name.Present, "description": changes.Description.Present,
		"permissions": changes.Permissions.Present, "is_default": changes.IsDefault.Present,
	} {
		if !field {
			t.Errorf("%s was mentioned as null and must count as mentioned", name)
		}
	}
	if changes.Name.Value != nil || changes.Permissions.Value != nil || changes.IsDefault.Value != nil {
		t.Errorf("a null carries no value: %+v", changes)
	}
}

// The three questions the route asks the body, each of which is a branch of the
// Python route.
func TestChangesAnswersTheRoutesThreeQuestions(t *testing.T) {
	cases := []struct {
		body        string
		permissions []string
		system      bool
		wantDefault bool
	}{
		{body: `{}`, permissions: nil, system: false, wantDefault: false},
		{body: `{"permissions":null}`, permissions: nil, system: false, wantDefault: false},
		{body: `{"permissions":[]}`, permissions: []string{}, system: false, wantDefault: false},
		{body: `{"permissions":["logs:read"]}`, permissions: []string{"logs:read"}, system: false, wantDefault: false},
		{body: `{"name":"x"}`, permissions: nil, system: true, wantDefault: false},
		{body: `{"description":"x"}`, permissions: nil, system: true, wantDefault: false},
		{body: `{"is_default":false}`, permissions: nil, system: true, wantDefault: false},
		{body: `{"is_default":null}`, permissions: nil, system: true, wantDefault: false},
		{body: `{"is_default":true}`, permissions: nil, system: true, wantDefault: true},
		{body: `{"is_default":"yes"}`, permissions: nil, system: true, wantDefault: true},
	}
	for _, tc := range cases {
		changes, failures, err := ReadChanges("application/json", []byte(tc.body))
		if err != nil || len(failures) > 0 {
			t.Fatalf("%s: %v %v", tc.body, failures, err)
		}
		if got := changes.RequestedPermissions(); len(got) != len(tc.permissions) {
			t.Errorf("%s: RequestedPermissions = %v, want %v", tc.body, got, tc.permissions)
		}
		if got := changes.TouchesSystemRoleFields(); got != tc.system {
			t.Errorf("%s: TouchesSystemRoleFields = %v, want %v", tc.body, got, tc.system)
		}
		if got := changes.AsksToBeDefault(); got != tc.wantDefault {
			t.Errorf("%s: AsksToBeDefault = %v, want %v", tc.body, got, tc.wantDefault)
		}
	}
}

// The wording of each refusal, as the Python tier gave it for the same body.
func TestReadChangesRefusesInPydanticsWords(t *testing.T) {
	for body, want := range map[string]string{
		`{"name":5}`:             "Input should be a valid string",
		`{"name":""}`:            "String should have at least 1 character",
		`{"description":5}`:      "Input should be a valid string",
		`{"permissions":"x"}`:    "Input should be a valid list",
		`{"permissions":[1]}`:    "Input should be a valid string",
		`{"is_default":0.5}`:     "Input should be a valid boolean",
		`{"is_default":"maybe"}`: "Input should be a valid boolean, unable to interpret input",
	} {
		_, failures, err := ReadChanges("application/json", []byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if len(failures) != 1 {
			t.Fatalf("%s: want one failure, got %+v", body, failures)
		}
		if failures[0].Message != want {
			t.Errorf("%s: message %q, want %q", body, failures[0].Message, want)
		}
	}
}

// The Owner role is found by its permissions, not by its name or its flag, so a
// custom role carrying the wildcard would be unchangeable too — which is what
// grants_full_access means.
func TestGrantsFullAccessReadsThePermissions(t *testing.T) {
	if !GrantsFullAccess(Role{Permissions: []string{"logs:read", "*"}}) {
		t.Error("the wildcard anywhere in the list grants full access")
	}
	if GrantsFullAccess(Role{Name: "Owner", IsSystemRole: true, Permissions: []string{"logs:read"}}) {
		t.Error("the name and the flag decide nothing")
	}
	if GrantsFullAccess(Role{}) {
		t.Error("a role with no permissions grants nothing")
	}
}
