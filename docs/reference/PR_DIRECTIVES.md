# PR Description Directives

This document describes the special directives that can be included in GitHub PR descriptions to control how the PR is posted to Slack.

## Directive Syntax

PR directives use the following format:

```
!review[s][:] [skip|no] [#channel_name] [@user_to_cc] [:emoji_name:]
```

**Note**: The colon after `!review` or `!reviews` is optional. Both formats work identically:
- `!review: #channel @user` (with colon)
- `!review #channel @user` (without colon)

### Universal Skip Directive

For skipping Slack notifications entirely and deleting any existing messages:

```
!review-skip
```

This directive works both proactively (prevents initial posting) and retroactively (deletes existing messages).

### Components

- **Magic string**: `!review` or `!reviews` (both forms work identically)
- **Skip directive**: `skip` or `no` - prevents the PR from being posted to Slack AND deletes existing messages (same as `!review-skip`)
- **Channel override**: `#channel_name` - overrides the default channel for posting
- **User CC**: `@user_to_cc` - mentions additional users in the Slack message (triggers real Slack notifications for registered users)  
- **Custom emoji**: `:emoji_name:` - overrides the default size-based emoji with a custom one

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
or
```
!review skip
```

### Channel Override
```
!review: #dev-team
```
or
```
!review #dev-team
```

### User CC
```
!review: @john.doe
```
or
```
!review @john.doe
```

### Custom Emoji
```
!review: :rocket:
```
or
```
!review :sparkles:
```

### Combined Usage
```
!review: #dev-team @jane.smith
```
or
```
!review #dev-team @jane.smith
```

```
!reviews: @team-lead #engineering
```
or
```
!reviews @team-lead #engineering
```

```
!review: :fire: #dev-team @reviewer
```
or
```
!review :fire: #dev-team @reviewer
```

### Skip with Other Components (Skip Takes Precedence)
```
!review: skip #dev-team @someone
```
This will skip posting entirely AND delete existing messages, ignoring the channel and user directives.

### Universal Skip
```
!review-skip
```
This will prevent the PR from being posted to Slack AND delete all existing Slack messages for this PR across all channels and workspaces. Use this when you want to completely remove a PR from Slack notifications.

## Directive Processing

If multiple `!review` or `!reviews` directives are present in the same PR description, the **last one wins** for each component (channel, user CC, emoji, skip).

## User Mentions

The `@user_to_cc` directive supports intelligent user mention resolution:

- **For registered users**: If the GitHub username matches a user who has connected their GitHub account to the bot and is verified in the same Slack workspace, the mention will use the proper Slack format `<@slackUserID>` which triggers a real Slack notification.

- **For unregistered users**: If no matching user is found in the database, the mention falls back to plain text format `@username` which appears as text but doesn't trigger Slack notifications.

This ensures that:
- Team members who have set up their GitHub integration will receive proper Slack notifications
- External contributors or users who haven't set up integration will still be mentioned visually in the message
- The directive works consistently regardless of user registration status

## Implementation Notes

- Directives are case-insensitive for the magic string (`!REVIEW`, `!Review`, `!REVIEW-SKIP`, etc. all work)
- The colon after `!review` or `!reviews` is optional - both `!review:` and `!review` work identically
- Channel names must start with `#` and contain only alphanumeric characters, hyphens, and underscores
- User mentions must start with `@` and should use GitHub usernames
- Custom emojis must be in the format `:emoji_name:` and override the default size-based emoji
- Both `!review: skip` and `!review-skip` now work identically - they prevent posting AND delete existing messages
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

```markdown
## Description
This PR adds new feature Y.

!review #team-beta @reviewer2

## Testing
- [x] Integration tests pass
```