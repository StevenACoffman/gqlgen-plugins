package graphqltools

import (
	"context"
	"github.com/vektah/gqlparser/v2"
	"os"
	"strings"
	"testing"

	"github.com/Khan/webapp/dev/khantest"
	"github.com/Khan/webapp/pkg/lib"
	"github.com/vektah/gqlparser/v2/ast"
)

type replaceSuite struct{ khantest.Suite }

// Additional directives used in tests. The @test directive is used to verify
// that non-@replaces directives are retained in the appropriate places.
const otherDirectiveSource = `
	directive @test
		on FIELD_DEFINITION | INPUT_FIELD_DEFINITION | OBJECT | UNION | ENUM | ENUM_VALUE | INPUT_OBJECT | INTERFACE | ARGUMENT_DEFINITION

	directive @key(
		fields: String!
	) on OBJECT
`

var replacesDirecticeSource string

func parse(input string) (*ast.Schema, error) {
	if replacesDirecticeSource == "" {
		path := lib.KARootJoin(
			context.Background(), "pkg", "graphql", "shared-schemas", "replaces_directive.graphql")
		sourceBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		replacesDirecticeSource = string(sourceBytes)
	}
	input = replacesDirecticeSource + otherDirectiveSource + input
	schema, err := gqlparser.LoadSchema(&ast.Source{Input: input})
	if err != nil {
		return nil, err
	}
	return schema, nil
}

func (suite *replaceSuite) TestFieldName() {
	schema, err := parse(`
		type Course @test {
			kaLocale: String @replaces(name: "locale") @test
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend type Course {
    locale: String @test @deprecated(reason: "Replaced by kaLocale.") @goField(name: "DeprecatedLocale")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestFieldNameAndType() {
	schema, err := parse(`
		type Classroom { id: String! }
		type User {
			classrooms: [Classroom!] @replaces(name: "studentLists", type: "StudentList")
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend type User {
    studentLists: [StudentList!] @deprecated(reason: "Replaced by classrooms.") @goField(name: "DeprecatedStudentLists")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestFederationKeyFieldEmitsOldKey() {
	schema, err := parse(`
		type UserKaLocaleCourse @key(fields: "id kaLocale kaid") {
			id: String!
			kaid: String!
			kaLocale: String @replaces(name: "locale")
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend type UserKaLocaleCourse @key(fields: "id locale kaid") {
    locale: String @deprecated(reason: "Replaced by kaLocale.") @goField(name: "DeprecatedLocale")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestArgumentName() {
	schema, err := parse(`
		type Classroom { id: String! }
		type User {
			classroom(id: String!, teacherKaid: String! @replaces(name: "coachKaid") @test): Classroom @replaces(name: "studentList")
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend type User {
    studentList(id: String!, coachKaid: String! @test): Classroom @deprecated(reason: "Replaced by classroom.") @goField(name: "DeprecatedStudentList")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestArgumentNameAndType() {
	schema, err := parse(`
		scalar Kaid
		type Classroom { id: String! }
		type User {
			classroom(id: String!, teacherKaid: Kaid! @replaces(name: "coachKaid", type: "String")): Classroom @replaces(name: "studentList")
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend type User {
    studentList(id: String!, coachKaid: String!): Classroom @deprecated(reason: "Replaced by classroom.") @goField(name: "DeprecatedStudentList")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestFieldMustBeReplacedIfArgumentReplaced() {
	schema, err := parse(`
		type Classroom { id: String! }
		type User {
			classroom(id: String!, teacherKaid: String! @replaces(name: "coachKaid")): Classroom
		}
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "@replaces directive on arguments can only be used on renamed fields")
}

func (suite *replaceSuite) TestObjectName() {
	schema, err := parse(`
		type Classroom @replaces(name: "StudentList") @test {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by Classroom."""
type StudentList @test {
    id: String!
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

// This test verifies that the @replaces directive is removed on field
// arguments in cases when the type the field is on is also renamed.
func (suite *replaceSuite) TestObjectNameAndArgumentName() {
	schema, err := parse(`
		type CourseMasterAssignment {
			id: ID!
		}
		enum SomeFilter {
			FILTER_ONE
			FILTER_TWO
		}
		type Classroom @replaces(name: "StudentList") @test {
			courseMasteryAssignments(filter: SomeFilter! @replaces(name: "oldFilter")): [CourseMasterAssignment!] @replaces(name: "subjectMasterAssignments")
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by Classroom."""
type StudentList @test {
    courseMasteryAssignments(filter: SomeFilter!): [CourseMasterAssignment!]
}

extend type Classroom {
    subjectMasterAssignments(oldFilter: SomeFilter!): [CourseMasterAssignment!] @deprecated(reason: "Replaced by courseMasteryAssignments.") @goField(name: "DeprecatedSubjectMasterAssignments")
}

extend type StudentList {
    subjectMasterAssignments(oldFilter: SomeFilter!): [CourseMasterAssignment!] @deprecated(reason: "Replaced by courseMasteryAssignments.") @goField(name: "DeprecatedSubjectMasterAssignments")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestObjectRetainsExistingDescription() {
	schema, err := parse(`
		"""This is a classroom."""
		type Classroom @replaces(name: "StudentList") @test {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""
This is a classroom.
Deprecated: Replaced by Classroom.
"""
type StudentList @test {
    id: String!
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestExtendedDefinitionStaysExtendedAndOmitsDescription() {
	schema, err := parse(`
		extend type Classroom @replaces(name: "StudentList") @test {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend type StudentList @test {
    id: String!
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestObjectCanNotUseType() {
	schema, err := parse(`
		type Classroom @replaces(name: "StudentList", type: "StudentList") {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "@replaces directive on definitions can only use `name` argument")
}

func (suite *replaceSuite) TestReplacedFieldOnReplacedObject() {
	schema, err := parse(`
		type Classroom @replaces(name: "StudentList") @test {
			id: String!
			teacherKaid: String! @replaces(name: "coachKaid") @test
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by Classroom."""
type StudentList @test {
    id: String!
    teacherKaid: String! @test
}

extend type Classroom {
    coachKaid: String! @test @deprecated(reason: "Replaced by teacherKaid.") @goField(name: "DeprecatedCoachKaid")
}

extend type StudentList {
    coachKaid: String! @test @deprecated(reason: "Replaced by teacherKaid.") @goField(name: "DeprecatedCoachKaid")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestInputObjectName() {
	schema, err := parse(`
		input NewInput @replaces(name: "OldInput") @test {
			arg: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by NewInput."""
input OldInput @test {
    arg: String!
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestInputObjectCanNotUseType() {
	schema, err := parse(`
		input NewInput @replaces(name: "OldInput", type: "OldInput") {
			arg: String!
		}
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "@replaces directive on definitions can only use `name` argument")
}

func (suite *replaceSuite) TestInputObjectFieldName() {
	schema, err := parse(`
		input SomeInput {
			newArg: String @replaces(name: "oldArg", treatZeroAsUnset: true) @test
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend input SomeInput {
    """Deprecated: Replaced by newArg."""
    oldArg: String @test @goField(name: "DeprecatedOldArg")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestInputObjectFieldNameTreatZeroAsUnsetCanBeFalse() {
	schema, err := parse(`
		input SomeInput {
			newArg: String @replaces(name: "oldArg", treatZeroAsUnset: false) @test
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend input SomeInput {
    """Deprecated: Replaced by newArg."""
    oldArg: String @test @goField(name: "DeprecatedOldArg")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestInputObjectNonListFieldNameMustSpecifyTreatZeroAsUnset() {
	schema, err := parse(`
		input SomeInput {
			newArg: String @replaces(name: "oldArg") @test
		}
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "@replaces directive on non-list input fields must include treatZeroAsUnset:true or treatZeroAsUnset:false")
}

func (suite *replaceSuite) TestInputObjectFieldTreatZeroAsUnsetNotRequiredOnLists() {
	schema, err := parse(`
		input SomeInput {
			newArg: [String!] @replaces(name: "oldArg") @test
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend input SomeInput {
    """Deprecated: Replaced by newArg."""
    oldArg: [String!] @test @goField(name: "DeprecatedOldArg")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestInputObjectFieldNameAndType() {
	schema, err := parse(`
		input SomeInput {
			newArg: String @replaces(name: "oldArg", type: "Int", treatZeroAsUnset: true) @test
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend input SomeInput {
    """Deprecated: Replaced by newArg."""
    oldArg: Int @test @goField(name: "DeprecatedOldArg")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestInputObjectFieldMustBeNullable() {
	schema, err := parse(`
		input SomeInput {
			newArg: String! @replaces(name: "oldArg", treatZeroAsUnset: true) @test
		}
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "input fields using the @replaces directive must be nullable")
}

func (suite *replaceSuite) TestInterfaceName() {
	schema, err := parse(`
		interface CurationNode @replaces(name: "Topic") @test {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by CurationNode."""
interface Topic @test {
    id: String!
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestInterfaceCanNotUseType() {
	schema, err := parse(`
		interface CurationNode @replaces(name: "Topic", type: "Topic") {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "@replaces directive on definitions can only use `name` argument")
}

func (suite *replaceSuite) TestInterfaceField() {
	schema, err := parse(`
		interface CurationNode {
			kaLocale: String @replaces(name: "locale") @test
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend interface CurationNode {
    locale: String @test @deprecated(reason: "Replaced by kaLocale.") @goField(name: "DeprecatedLocale")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestObjectsThatImplementReplacedInterfaceShouldBeExtended() {
	schema, err := parse(`
		interface CurationNode @replaces(name: "Topic") {
			id: String!
		}

		type Domain implements CurationNode @test {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by CurationNode."""
interface Topic {
    id: String!
}

extend type Domain implements Topic

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestReplacedInterfaceOnReplacedObject() {
	schema, err := parse(`
		interface CurationNode @replaces(name: "Topic") {
			id: String!
		}

		type Domain implements CurationNode @replaces(name: "OldDomain") @test {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by Domain."""
type OldDomain implements CurationNode @test {
    id: String!
}

"""Deprecated: Replaced by CurationNode."""
interface Topic {
    id: String!
}

extend type Domain implements Topic

extend type OldDomain implements Topic

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestUnionName() {
	schema, err := parse(`
		type Domain { id: String! }
		type Course { id: String! }
		union CurationNodeChild @replaces(name: "TopicChildren") @test = Domain | Course
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by CurationNodeChild."""
union TopicChildren @test = Domain | Course

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestUnionCanNotUseType() {
	schema, err := parse(`
		type Domain { id: String! }
		type Course { id: String! }
		union CurationNodeChild @replaces(name: "TopicChildren", type: "TopicChildren") = Domain | Course
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "@replaces directive on definitions can only use `name` argument")
}

func (suite *replaceSuite) TestUnionWithReplacedMembersShouldBeExtended() {
	schema, err := parse(`
		union ClassroomStuff @test = Classroom

		type Classroom @replaces(name: "StudentList") {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by Classroom."""
type StudentList {
    id: String!
}

extend union ClassroomStuff = StudentList

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestUnionMemberOnReplacedUnion() {
	schema, err := parse(`
		union ClassroomStuff @replaces(name: "OldClassroomStuff") = Classroom

		type Classroom @replaces(name: "StudentList") {
			id: String!
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by ClassroomStuff."""
union OldClassroomStuff = Classroom

"""Deprecated: Replaced by Classroom."""
type StudentList {
    id: String!
}

extend union ClassroomStuff = StudentList

extend union OldClassroomStuff = StudentList

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestEnumName() {
	schema, err := parse(`
		enum ContentKind @replaces(name: "TopicKind") @test {
			DOMAIN
			COURSE
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by ContentKind."""
enum TopicKind @test {
    DOMAIN
    COURSE
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestEnumCanNotUseType() {
	schema, err := parse(`
		enum ContentKind @replaces(name: "TopicKind", type: "TopicKind") {
			DOMAIN
			COURSE
		}
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "@replaces directive on definitions can only use `name` argument")
}

func (suite *replaceSuite) TestEnumValue() {
	schema, err := parse(`
		enum ContentKind {
			DOMAIN
			COURSE @replaces(name: "TOPIC") @test
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
extend enum ContentKind {
    TOPIC @test @deprecated(reason: "Replaced by COURSE.")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestReplacedEnumValueOnReplacedEnum() {
	schema, err := parse(`
		enum ContentKind @replaces(name: "OldContentKind") {
			DOMAIN
			COURSE @test @replaces(name: "TOPIC")
		}
	`)
	suite.Require().NoError(err)

	updates, err := GetReplacesDirectiveUpdates(schema)
	suite.Require().NoError(err)

	expected := strings.TrimLeft(`
"""Deprecated: Replaced by ContentKind."""
enum OldContentKind {
    DOMAIN
    COURSE @test
}

extend enum ContentKind {
    TOPIC @test @deprecated(reason: "Replaced by COURSE.")
}

extend enum OldContentKind {
    TOPIC @test @deprecated(reason: "Replaced by COURSE.")
}

`, "\n")

	suite.Require().Equal(expected, updates)
}

func (suite *replaceSuite) TestEnumValueCanNotUseType() {
	schema, err := parse(`
		enum ContentKind {
			DOMAIN
			COURSE @replaces(name: "TOPIC", type: "TOPIC")
		}
	`)
	suite.Require().NoError(err)

	_, err = GetReplacesDirectiveUpdates(schema)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "@replaces directive on enum values can only use `name` argument")
}

func TestReplacesDirective(t *testing.T) {
	khantest.Run(t, new(replaceSuite))
}

type definitionExtendSuite struct{ khantest.Suite }

func (suite *definitionExtendSuite) TestDefinitionHasExtends() {
	tests := []struct {
		name           string
		input          string
		definitionName string
		hasExtend      bool
	}{
		{
			name:           "Extend at beginning of input",
			input:          "extend type StudentList { kaid: String! }",
			definitionName: "StudentList",
			hasExtend:      true,
		},
		{
			name:           "Extend NOT at beginning of input",
			input:          "enum ContentKind { ARTICLE VIDEO EXERCISE } extend type StudentList { kaid: String! }",
			definitionName: "StudentList",
			hasExtend:      true,
		},
		{
			name:           "Extend with extra white space",
			input:          " extend   type     StudentList { kaid: String! }",
			definitionName: "StudentList",
			hasExtend:      true,
		},
		{
			name:           "Extend with really long type name",
			input:          "extend type SomeReallyReallyLongTypeName { kaid: String! }",
			definitionName: "SomeReallyReallyLongTypeName",
			hasExtend:      true,
		},

		{
			name:           "No extend, at beginning of input",
			input:          "type StudentList { kaid: String! }",
			definitionName: "StudentList",
			hasExtend:      false,
		},
		{
			name:           "No extend, NOT at beginning of input",
			input:          "enum ContentKind { ARTICLE VIDEO EXERCISE } type StudentList { kaid: String! }",
			definitionName: "StudentList",
			hasExtend:      false,
		},
		{
			name:           "Interface",
			input:          "extend interface CurationNode { kind: String! }",
			definitionName: "CurationNode",
			hasExtend:      true,
		},
		{
			name:           "Input object",
			input:          "extend input CurationNode { kind: String! }",
			definitionName: "CurationNode",
			hasExtend:      true,
		},
		{
			name:           "Extend in comment on previous line",
			input:          "#extend\ntype StudentList { kaid: String! }",
			definitionName: "StudentList",
			hasExtend:      false,
		},
	}

	for _, test := range tests {
		test := test // fix scoping
		suite.Run(test.name, func() {
			schema, err := gqlparser.LoadSchema(&ast.Source{Input: test.input})
			suite.Require().NoError(err)

			definition := schema.Types[test.definitionName]
			suite.Require().NotNil(definition, "Type NOT FOUND in schema: %s", test.definitionName)

			hasExtend := _definitionHasExtends(definition)
			suite.Require().Equal(test.hasExtend, hasExtend)
		})
	}
}

func TestDefinitionHasExtends(t *testing.T) {
	khantest.Run(t, new(definitionExtendSuite))
}
