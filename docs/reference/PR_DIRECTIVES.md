# PR Description Directives

This document describes the special directives that can be included in GitHub PR descriptions to control how the PR is posted to Slack.

## Directive Syntax

PR directives use the following format:

```
!review[s]: [skip|no] [#channel_name] [@user_to_cc]
```

### Universal Skip Directive

For skipping Slack notifications entirely and deleting any existing messages:

```
!review-skip
```

This directive works both proactively (prevents initial posting) and retroactively (deletes existing messages).

### Components

- **Magic string**: `!review` or `!reviews` (both forms work identically)
- **Skip directive**: `skip` or `no` - prevents the PR from being posted to Slack
- **Channel override**: `#channel_name` - overrides the default channel for posting
- **User CC**: `@user_to_cc` - tags additional users in the Slack message

### Order and Combinations

- All components are optional except the magic string
- Components can appear in any order after the colon
- Multiple directives of the same type (last one wins for channel/user, skip always applies)
- Whitespace between components is ignored

## Examples

### Skip Posting
```
!review: skip
```
or
```
!reviews: no
```

### Channel Override
```
!review: #dev-team
```

### User CC
```
!review: @john.doe
```

### Combined Usage
```
!review: #dev-team @jane.smith
```

```
!reviews: @team-lead #engineering
```

### Skip with Other Components (Skip Takes Precedence)
```
!review: skip #dev-team @someone
```
This will skip posting entirely, ignoring the channel and user directives.

### Universal Skip
```
!review-skip
```
This will prevent the PR from being posted to Slack AND delete all existing Slack messages for this PR across all channels and workspaces. Use this when you want to completely remove a PR from Slack notifications.

## Directive Processing

If multiple `!review` or `!reviews` directives are present in the same PR description, the **last one wins** for each component (channel, user CC, skip).

## Implementation Notes

- Directives are case-insensitive for the magic string (`!REVIEW`, `!Review`, `!REVIEW-SKIP`, etc. all work)
- Channel names must start with `#` and contain only alphanumeric characters, hyphens, and underscores
- User mentions must start with `@` and follow Slack username conventions
- Invalid directives are ignored with warnings logged
- `!review-skip` takes precedence over all other directives and only triggers message deletion (no parsing of other components)

## Parsing Behavior

The system parses PR descriptions looking for lines containing the `!review` or `!reviews` pattern. The directive can appear:

- On its own line
- At the start of a line
- Mixed with other text (though this is not recommended)

### Valid Placement Examples

```markdown
## Description
This PR adds new feature X.

!review: #team-alpha @reviewer1

## Testing
- [x] Unit tests pass
```

```markdown
!review: skip

This is a work-in-progress PR, don't notify yet.
```

```markdown
Some text here. !review: #dev-team @lead
More text after.
```