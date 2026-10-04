package models

// The groups this site gives meaning to, in the identity provider. Two are
// global and one of each per collective and per organisation:
//
//	<app>-admins                           administer everything
//	<app>-users                            sign in, read; be added to things
//	<app>-collective-<slug>-admins         edit the collective, its members, its authors
//	<app>-collective-<slug>-authors        publish its topics, news and actions
//	<app>-organisation-<slug>-admins       edit the organisation, its people
//	<app>-organisation-<slug>-members      its people
//
// **The names are a contract.** They are what Authentik stores and what
// somebody reading the directory has to recognise, so the per-entry ones are
// built once, when the entry is created, and stored on it.

// UsersGroupSuffix completes the group of everybody who may use the console.
const UsersGroupSuffix = "-users"

// UsersGroupName is the group of everybody who may sign in to the console.
func UsersGroupName(app string) string { return app + UsersGroupSuffix }

// CollectiveGroups names a collective's two groups.
func CollectiveGroups(app, slug string) (admins, authors string) {
	prefix := app + "-collective-" + slug
	return prefix + "-admins", prefix + "-authors"
}

// OrganisationGroups names an organisation's two groups.
func OrganisationGroups(app, slug string) (admins, members string) {
	prefix := app + "-organisation-" + slug
	return prefix + "-admins", prefix + "-members"
}
