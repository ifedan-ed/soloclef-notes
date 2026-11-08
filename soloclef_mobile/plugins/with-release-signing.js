/**
 * Sign local Android release builds with a keystore kept outside the repo.
 *
 * The generated android/app/build.gradle signs "release" with the debug key.
 * This plugin adds a "release" signing config that reads the keystore from
 * Gradle properties (pass them with -P or ~/.gradle/gradle.properties):
 *
 *   SOLOCLEF_RELEASE_STORE_FILE      absolute path to the .jks/.keystore
 *   SOLOCLEF_RELEASE_STORE_PASSWORD
 *   SOLOCLEF_RELEASE_KEY_ALIAS
 *   SOLOCLEF_RELEASE_KEY_PASSWORD
 *
 * Without them the build falls back to the debug key, so `expo run:android`
 * and EAS (which injects its own signing) keep working unchanged.
 */
const { withAppBuildGradle } = require("expo/config-plugins");

const MARKER = "// soloclef-release-signing";

function addReleaseSigning(gradle) {
	if (gradle.includes(MARKER)) return gradle;
	const releaseConfig = `
        release { ${MARKER}
            if (project.hasProperty('SOLOCLEF_RELEASE_STORE_FILE')) {
                storeFile file(SOLOCLEF_RELEASE_STORE_FILE)
                storePassword SOLOCLEF_RELEASE_STORE_PASSWORD
                keyAlias SOLOCLEF_RELEASE_KEY_ALIAS
                keyPassword SOLOCLEF_RELEASE_KEY_PASSWORD
            }
        }`;
	let out = gradle.replace(/signingConfigs\s*\{/, (match) => `${match}${releaseConfig}`);
	out = out.replace(
		/(buildTypes\s*\{[\s\S]*?release\s*\{[\s\S]*?)signingConfig signingConfigs\.debug/,
		`$1signingConfig project.hasProperty('SOLOCLEF_RELEASE_STORE_FILE') ? signingConfigs.release : signingConfigs.debug`
	);
	return out;
}

module.exports = function withReleaseSigning(config) {
	return withAppBuildGradle(config, (mod) => {
		mod.modResults.contents = addReleaseSigning(mod.modResults.contents);
		return mod;
	});
};

module.exports.addReleaseSigning = addReleaseSigning;
