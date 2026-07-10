import {createRouter, createWebHistory} from 'vue-router'
import Home from '@/views/Home'
import EndpointDetailRouter from "@/views/EndpointDetailRouter";
import SuiteDetails from '@/views/SuiteDetails';

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
        path: '/suites/:key',
        name: 'SuiteDetails',
        component: SuiteDetails
    }
];

const router = createRouter({
    history: createWebHistory(process.env.BASE_URL),
    routes
});

export default router;
