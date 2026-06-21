package linear

const candidatesQuery = `query Candidates($projectSlug: String!, $states: [String!], $after: String) { issues(first: 50, after: $after, filter: { project: { slugId: { eq: $projectSlug } }, state: { name: { in: $states } } }) { pageInfo { hasNextPage endCursor } nodes { id identifier title description priority branchName url createdAt updatedAt state { name } labels { nodes { name } } relations { nodes { type relatedIssue { id identifier state { name } } } } } } }`
const statesQuery = `query States($ids: [ID!]!) { issues(filter: { id: { in: $ids } }) { nodes { id identifier title state { name } labels { nodes { name } } } } }`
