// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import react from '@astrojs/react';
import tailwindcss from '@tailwindcss/vite';
import { isPreview, siteConfiguration } from './scripts/site-config.mjs';
import { readCatalog } from './scripts/release-catalog.mjs';

const publication = siteConfiguration();
const releaseCatalog = await readCatalog();

export default defineConfig({
	site: publication.origin,
	base: publication.base,
	trailingSlash: 'always',
	output: 'static',
	vite: {
		plugins: [tailwindcss()],
		define: { __SODAPOP_RELEASE_CATALOG__: JSON.stringify(releaseCatalog) },
	},
	integrations: [
		react(),
		starlight({
			title: 'Sodapop',
			disable404Route: true,
			description: 'Get Sodapop running in your project, learn the commands, and choose your level of fizz.',
			components: {
				Banner: './src/components/docs/PreviewBanner.astro',
				MarkdownContent: './src/components/docs/MarkdownContent.astro',
			},
			logo: {
				light: '../images/sodapop-lockup-on-light.svg',
				dark: '../images/sodapop-lockup-on-dark.svg',
				replacesTitle: true,
			},
			favicon: '/assets/brand/sodapop-favicon.svg',
			head: [
				{ tag: 'meta', attrs: { name: 'sodapop-build', content: isPreview() ? 'preview' : 'public' } },
				{ tag: 'meta', attrs: { name: 'robots', content: isPreview() ? 'noindex' : 'index, follow' } },
			],
			customCss: ['./src/styles/docs.css'],
			social: [{ icon: 'github', label: 'Sodapop on GitHub', href: 'https://github.com/VeVarunSharma/sodapop' }],
			sidebar: [
				{
					label: 'Open your first can',
					items: [
						{ label: 'Welcome', slug: 'docs' },
						{ label: 'Getting started', slug: 'docs/getting-started' },
						{ label: 'Installation', slug: 'docs/installation' },
					],
				},
				{
					label: 'Make it yours',
					items: [
						{ label: 'Commands & shortcuts', slug: 'docs/commands' },
						{ label: 'Customization', slug: 'docs/customization' },
						{ label: 'Permissions & privacy', slug: 'docs/permissions-and-privacy' },
						{ label: 'Troubleshooting', slug: 'docs/troubleshooting' },
					],
				},
				{ label: 'For builders', items: [{ autogenerate: { directory: 'docs/development' } }] },
			],
		}),
	],
});
