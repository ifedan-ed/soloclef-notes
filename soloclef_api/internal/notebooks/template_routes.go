package notebooks

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/starlettepath"
)

// The three report-template routes. A template's edit is the account's, not
// a notebook's: no notebook is looked for, and the edit is used inside any
// of the account's notebooks.

// RegisterTemplateList serves the list.
func (r Routes) RegisterTemplateList(api huma.API) {
	register[noPath](api, r, http.MethodGet, TemplatesPath,
		"list-report-templates", "The report templates, with the caller's edits", r.sendTemplates)
}

// sendTemplates answers with the templates: every seed, with the caller's
// own edit laid over it.
func (r Routes) sendTemplates(x *exchange) {
	templates, err := shownTemplates(x.ctx.Context(), r.Database, x.caller.UserID)
	if err != nil {
		x.fail("could not read the caller's report templates", err)
		return
	}
	x.send(http.StatusOK, templates)
}

// seededTemplate reads the template's key off the path and answers the 404
// itself for one that is not seeded. The key is whatever one segment held,
// decoded once; the caller has already turned away a path the router should
// not have matched.
//
// The 404 comes before anything is read or written, so a stored edit of a
// key that used to be seeded cannot be written through here.
func (x *exchange) seededTemplate() (*seedTemplate, bool) {
	seed := seedFor(starlettepath.AsDecodedByTheServer(x.ctx.Param("template_key")))
	if seed == nil {
		x.notFound(templateAbsent)
		return nil, false
	}
	return seed, true
}

// RegisterTemplateSave serves the save.
func (r Routes) RegisterTemplateSave(api huma.API) {
	register[templatePath](api, r, http.MethodPut, TemplatePath,
		"save-report-template", "Save the caller's edit of a report template", r.saveTemplate)
}

// saveTemplate stores the caller's edit and answers the list as it now is.
// A body that does not validate is the 422 whatever the key, as every other
// route judges its parameters before it looks anything up.
func (r Routes) saveTemplate(x *exchange) {
	edit, failures, err := readTemplateEdit(
		x.ctx.Header("Content-Type"), requestbody.Buffered(x.ctx.Context()))
	if err != nil {
		x.fail("a body reached the route that the body reader should have turned away", err)
		return
	}
	if x.unroutable("template_key") {
		return
	}
	if len(failures) > 0 {
		x.invalid(failures)
		return
	}
	seed, ok := x.seededTemplate()
	if !ok {
		return
	}
	title, description, prompt := storedEdit(seed, edit)
	if err := New(r.Database).SaveTemplate(x.ctx.Context(), SaveTemplateParams{
		UserID:      x.caller.UserID,
		TemplateKey: seed.key,
		Title:       title,
		Description: description,
		Prompt:      prompt,
		WrittenAt:   r.now(),
	}); err != nil {
		x.fail("could not save the report template", err)
		return
	}
	r.sendTemplates(x)
}

// RegisterTemplateReset serves the reset.
func (r Routes) RegisterTemplateReset(api huma.API) {
	register[templatePath](api, r, http.MethodDelete, TemplatePath,
		"reset-report-template", "Discard the caller's edit of a report template", r.resetTemplate)
}

// resetTemplate discards the caller's edit, so the seed shows again.
// Resetting a template nobody edited is not an error.
func (r Routes) resetTemplate(x *exchange) {
	if x.unroutable("template_key") {
		return
	}
	seed, ok := x.seededTemplate()
	if !ok {
		return
	}
	if err := New(r.Database).ResetTemplate(x.ctx.Context(),
		ResetTemplateParams{UserID: x.caller.UserID, TemplateKey: seed.key}); err != nil {
		x.fail("could not reset the report template", err)
		return
	}
	r.sendTemplates(x)
}
