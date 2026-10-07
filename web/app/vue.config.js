// Note: The fs.Stats deprecation warning is from Vue CLI's webpack dependencies
// which are not yet compatible with Node.js v23. This is suppressed in the build
// script. All user dependencies have been updated to their latest versions.
// Consider migrating to Vite for better Node.js v23+ compatibility.
module.exports = {
	filenameHashing: false,
	productionSourceMap: false,
	outputDir: '../static',
	publicPath: '/',
	devServer: {
		port: 8081,
		https: false,
		// The app is a single-page application with client-side routes, so a
		// deep link like /jira has no file behind it. Without this the dev
		// server 404s on every route except "/", which makes it useless for
		// working on a page other than the home view. devServer-only; it has
		// no effect on the production build.
		historyApiFallback: true,
		client: {
			webSocketURL:'auto://0.0.0.0/ws'
		},
		proxy: {
			'^/api|^/css|^/oicd': {
				target: "http://localhost:8080",
				changeOrigin: true,
				secure: false,
			}
		}
	}
}
