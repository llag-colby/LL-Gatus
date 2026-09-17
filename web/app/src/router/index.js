import {createRouter, createWebHistory} from 'vue-router'
import Home from '@/views/Home'
import EndpointDetailRouter from "@/views/EndpointDetailRouter";
import SuiteDetails from '@/views/SuiteDetails';
import JiraDetails from '@/views/JiraDetails';
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
