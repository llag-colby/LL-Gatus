import {createRouter, createWebHistory} from 'vue-router'
import Home from '@/views/Home'
import EndpointDetailRouter from "@/views/EndpointDetailRouter";
import SuiteDetails from '@/views/SuiteDetails';
import JiraDetails from '@/views/JiraDetails';
import S1Details from '@/views/S1Details';
import SiteOverview from '@/views/SiteOverview';
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
        // SentinelOne threat console. Threats are this product's tickets:
        // they carry an incident status, an analyst verdict and a mitigation
        // state, so the page is shaped like the service-desk board.
        path: '/s1',
        name: 'SentinelOne',
        component: S1Details
    },
    {
        // Which checks are paused, globally, plus a note on where the
        // per-browser preferences live.
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
