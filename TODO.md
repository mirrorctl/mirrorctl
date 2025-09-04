TODO
====

1. ~~Validate mirror configuration~~
1. Make it clear that the Release files being downloaded are indeed Release files and not downloaded packages
1. Create an SBOM for the utility
1. Ensure that we have good test coverage
1. Updated README & documentation
1. Container for the application
1. Example of testing w/o a PGP key, adding a PGP key to the command, and adding it to the config.
1. Test a flat mirror
1. Ability to specify the 'tmp' directory
1. CONTRIBUTING.md document
1. Write good godoc comments
1. Have a collection of mirror configs that people can use in the docs (but don't provide the main
   Ubuntu / Debian ones. Encourage folks to find an upstream mirror local to them). This is more
   for things like Docker, VS Code, etc.). Link out to their setup instructions instead. Let the
   project maintainers do their thing.
1. Automatically build packages, including the shell completion. 'go-releaser' or something?
1. Allow for a custom script to be run after the mirror sync is complete. This is similar to what
   is provided in apt-mirror's 'postmirror_script' functionality. I would reuse the same name.
1. Make "tries" and "retry-on-http-error" options configurable. See: https://github.com/apt-mirror/apt-mirror/pull/191


Maybe
-----

1. Ability to publish to alternate storage back ends (e.g., s3, r2, etc.)?
1. Validate a live repository. Would only work on full repos. Might not work / might be weird.
1. Ansible for build, deployment and setup.
1. Implement mirroring of yum repositories.
