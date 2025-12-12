# Pull Request: Merge revert-to-339728e into master

## 📝 Description

This pull request merges the branch `revert-to-339728e` into `master`. The `revert-to-339728e` branch is based on `master` and contains the project state reverted to commit `339728ea0deaae900b6d740bb089130536cd0d4d`.

## 🎯 Purpose

This PR is intended for reviewing and potentially rolling back changes to a previous state of the project. By merging this branch, the codebase will be restored to the state at commit `339728ea0deaae900b6d740bb089130536cd0d4d`, which introduced support and related projects sections to the documentation.

## 📌 Branch Details

- **Source Branch**: `revert-to-339728e`
- **Target Branch**: `master`
- **Revert Target Commit**: `339728ea0deaae900b6d740bb089130536cd0d4d`
- **Commit Message**: "Add support and related projects sections to documentation"

## 🔄 What This Revert Does

Reverting to commit `339728e` will roll back all changes made after:
- The implementation of the Go-based CLI
- Multiple TUI enhancements and improvements
- Version releases from v1.0.0 through v1.2.3
- Various refactoring and feature additions

The revert restores the project to the state where it had:
- Support section with links to GitHub Issues, Discussions, and Discord
- Related projects section detailing integrations
- Basic documentation structure

## ⚠️ Important Notes

This is a significant rollback that will undo many recent improvements and features. This PR should be carefully reviewed by all stakeholders before merging.

## ✅ Review Checklist

- [ ] Verify that reverting to this commit is intentional and necessary
- [ ] Confirm all stakeholders are aware of the rollback
- [ ] Review the list of changes that will be reverted
- [ ] Consider alternative approaches (e.g., selective reverts)
- [ ] Ensure proper communication plan for users affected by the rollback

## 🔗 Related Information

- Commit being reverted to: `339728ea0deaae900b6d740bb089130536cd0d4d`
- Branch created for review: `revert-to-339728e`
