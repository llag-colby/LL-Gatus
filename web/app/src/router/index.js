import {createRouter, createWebHistory} from 'vue-router'
import Home from '@/views/Home'
import EndpointDetailRouter from "@/views/EndpointDetailRouter";
import SuiteDetails from '@/views/SuiteDetails';
import JiraDetails from '@/views/JiraDetails';
import SiteOverview from '@/views/SiteOverview';
import TelemetryConsole from '@/views/TelemetryConsole';
import SettingsView from '@/views/SettingsView';

const routes = [
    {
        path: '/',
        name: 'Home',
        component: Home
    },
    {
        path: '/endpoints/:key',
        name: 'EndpointDetails',
        component: EndpointDetailRouter,
    },
    {
        // Whole-site drill-in (the Overall row on a location card). Keyed by
        // endpoint `name`, which is what groups endpoints into a site.
        path: '/sites/:name',
        name: 'SiteOverview',
        component: SiteOverview,
    },
    {
        path: '/suites/:key',
        name: 'SuiteDetails',
        component: SuiteDetails
    },
    {
        path: '/jira',
        name: 'Jira',
        component: JiraDetails
    },
    {
        // LL-Telemetry operations console. The page itself is served by Gatus at
        // /api/v1/telemetry/console and framed same-origin by this view.
        path: '/ll-telemetry',
        name: 'Telemetry',
        component: TelemetryConsole
    },
    {
        // Account, users, monitoring overview and the role reference. The page
        // renders per role: the admin sections are absent, not disabled.
        path: '/settings',
        name: 'Settings',
        component: SettingsView
    }
];

const router = createRouter({
    history: createWebHistory(process.env.BASE_URL),
    routes
});

export default router;
